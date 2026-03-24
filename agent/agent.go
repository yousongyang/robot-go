package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	robot_case "github.com/atframework/robot-go/case"
	user_data "github.com/atframework/robot-go/data"
	"github.com/atframework/robot-go/report"
	report_impl "github.com/atframework/robot-go/report/impl"
)

// AgentConfig Agent 启动配置
type AgentConfig struct {
	MasterAddr string // Master HTTP 地址
	RedisAddr  string
	RedisPwd   string
	RedisDB    int
	AgentID    string // 唯一标识（默认 hostname+pid）
	GroupID    string // 组 ID（可选，Master 按组分发任务）
}

// Agent 分布式压测执行端（主动向 Master 拉取任务）
type Agent struct {
	cfg    AgentConfig
	writer *report_impl.RedisReportWriter
	client *http.Client
}

// NewAgent 创建 Agent 实例并连接 Redis
func NewAgent(cfg AgentConfig) (*Agent, error) {
	if cfg.AgentID == "" {
		host, _ := os.Hostname()
		cfg.AgentID = fmt.Sprintf("agent-%s-%d", host, os.Getpid())
	}

	redisClient, err := report_impl.NewRedisClient(cfg.RedisAddr, cfg.RedisPwd, cfg.RedisDB)
	if err != nil {
		return nil, err
	}

	return &Agent{
		cfg:    cfg,
		writer: report_impl.NewRedisReportWriter(redisClient, cfg.AgentID),
		client: &http.Client{Timeout: 40 * time.Second},
	}, nil
}

// Start 注册到 Master，然后进入长轮询循环（阻塞）
func (a *Agent) Start() error {
	if err := a.registerToMaster(); err != nil {
		log.Printf("[Agent] Warning: register to master failed: %v (will retry on heartbeat)", err)
	}

	go a.heartbeatLoop()

	log.Printf("[Agent] %s started, Master=%s, Redis=%s", a.cfg.AgentID, a.cfg.MasterAddr, a.cfg.RedisAddr)
	a.pollLoop()
	return nil
}

// pollLoop 持续向 Master 拉取任务并执行（阻塞，永不返回）
func (a *Agent) pollLoop() {
	for {
		task, err := a.pollTask()
		if err != nil {
			log.Printf("[Agent] poll error: %v, retry in 3s", err)
			time.Sleep(3 * time.Second)
			continue
		}
		if task == nil {
			// 204 No Content：master 无任务，poll 本身已包含 30s 等待，立即重试
			continue
		}
		a.executeTask(task)
	}
}

// pollTask 向 Master 拉取一个任务；返回 nil,nil 表示 204 当前无任务
func (a *Agent) pollTask() (*robot_case.AgentTask, error) {
	u, _ := url.Parse(a.cfg.MasterAddr + "/api/agent/tasks/next")
	q := u.Query()
	q.Set("agent_id", a.cfg.AgentID)
	u.RawQuery = q.Encode()

	resp, err := a.client.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from master poll", resp.StatusCode)
	}
	var task robot_case.AgentTask
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	return &task, nil
}

// executeTask 执行一个压测任务，将数据写入 Redis，并上报结果给 Master。
// 执行期间定期 flush 部分打点数据到 Redis 以支持实时预览，
// 同时轮询 Master 检查任务是否被取消。
func (a *Agent) executeTask(task *robot_case.AgentTask) {
	log.Printf("[Agent] Executing task: key=%s report=%s case=%d name=%s IDs=[%d,%d) QPS=%.1f",
		task.TaskKey, task.ReportID, task.CaseIndex, task.Params.CaseName,
		task.Params.OpenIDStart, task.Params.OpenIDEnd, task.Params.TargetQPS)

	tracer := report_impl.NewMemoryTracer()
	pressure := report_impl.NewMemoryPressureController()

	// 在线用户指标
	onlineMetricsCollector := report_impl.NewMemoryMetricsCollector()
	onlineMetricsCollector.Register("online_users", func() float64 {
		return float64(user_data.OnlineUserCount())
	})
	onlineMetricsCollector.Collect() // 立即采一次初始值
	onlineMetricsCollector.StartAutoCollect(time.Second)

	// 创建 cancel context，用于取消机制
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 定期 flush 部分数据到 Redis 和检查 cancel
	flushDone := make(chan struct{})
	go func() {
		defer close(flushDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 检查任务是否被取消
				if a.checkCancelled(task.ReportID) {
					cancel()
					return
				}
				// flush 部分打点数据
				a.flushPartialData(task.ReportID, task.Params.CaseName, tracer, pressure)
			}
		}
	}()

	errMsg := robot_case.RunCaseStressWithContext(ctx, task.Params, tracer, pressure)

	onlineMetricsCollector.StopAutoCollect()
	onlineMetricsCollector.Collect() // 最终采集一次

	// 停止 flush goroutine
	cancel()
	<-flushDone

	// 最终 flush 剩余数据
	tracings := tracer.Flush()
	var metricsData []*report.MetricsSeries

	snapshots := pressure.Snapshots()
	if len(snapshots) > 0 {
		var pressurePts, throttlePts, actualQPSPts []report.MetricsPoint
		for _, s := range snapshots {
			pressurePts = append(pressurePts, report.MetricsPoint{Timestamp: s.Timestamp, Value: float64(s.Level)})
			throttlePts = append(throttlePts, report.MetricsPoint{Timestamp: s.Timestamp, Value: s.ThrottleRatio})
			actualQPSPts = append(actualQPSPts, report.MetricsPoint{Timestamp: s.Timestamp, Value: s.ActualQPS})
		}
		metricsData = append(metricsData,
			&report.MetricsSeries{Name: "pressure_level", Labels: map[string]string{"agent": a.cfg.AgentID, "case": task.Params.CaseName}, Points: pressurePts},
			&report.MetricsSeries{Name: "throttle_ratio", Labels: map[string]string{"agent": a.cfg.AgentID, "case": task.Params.CaseName}, Points: throttlePts},
			&report.MetricsSeries{Name: "actual_qps", Labels: map[string]string{"agent": a.cfg.AgentID, "case": task.Params.CaseName}, Points: actualQPSPts},
		)
	}

	// online_users 指标（带 case 标签）
	onlineMetrics := onlineMetricsCollector.Flush()
	for _, s := range onlineMetrics {
		if s.Labels == nil {
			s.Labels = make(map[string]string)
		}
		s.Labels["agent"] = a.cfg.AgentID
		s.Labels["case"] = task.Params.CaseName
	}
	metricsData = append(metricsData, onlineMetrics...)

	if err := a.writer.WriteTracings(task.ReportID, tracings); err != nil {
		log.Printf("[Agent] write tracings error: %v", err)
	}
	if err := a.writer.WriteMetrics(task.ReportID, metricsData); err != nil {
		log.Printf("[Agent] write metrics error: %v", err)
	}
	if err := a.writer.BarrierACK(task.ReportID, task.CaseIndex); err != nil {
		log.Printf("[Agent] barrier ack error: %v", err)
	}

	log.Printf("[Agent] Task done: key=%s tracings=%d", task.TaskKey, len(tracings))

	if err := a.postResult(robot_case.AgentTaskResult{
		TaskKey:  task.TaskKey,
		Tracings: len(tracings),
		Error:    errMsg,
	}); err != nil {
		log.Printf("[Agent] post result error: %v", err)
	}
}

// flushPartialData 将内存中的部分打点数据 flush 到 Redis（增量写入）。
func (a *Agent) flushPartialData(reportID string, caseName string, tracer *report_impl.MemoryTracer, pressure *report_impl.MemoryPressureController) {
	// Flush 会清空内存缓冲，RPush 到 Redis 是增量累加
	tracings := tracer.Flush()
	if len(tracings) > 0 {
		if err := a.writer.WriteTracings(reportID, tracings); err != nil {
			log.Printf("[Agent] partial flush tracings error: %v", err)
		} else {
			log.Printf("[Agent] Partial flush: %d tracings written to Redis", len(tracings))
		}
	}

	snapshots := pressure.Snapshots()
	if len(snapshots) > 0 {
		var metricsData []*report.MetricsSeries
		var pressurePts, throttlePts, actualQPSPts []report.MetricsPoint
		for _, s := range snapshots {
			pressurePts = append(pressurePts, report.MetricsPoint{Timestamp: s.Timestamp, Value: float64(s.Level)})
			throttlePts = append(throttlePts, report.MetricsPoint{Timestamp: s.Timestamp, Value: s.ThrottleRatio})
			actualQPSPts = append(actualQPSPts, report.MetricsPoint{Timestamp: s.Timestamp, Value: s.ActualQPS})
		}
		metricsData = append(metricsData,
			&report.MetricsSeries{Name: "pressure_level", Labels: map[string]string{"agent": a.cfg.AgentID, "case": caseName}, Points: pressurePts},
			&report.MetricsSeries{Name: "throttle_ratio", Labels: map[string]string{"agent": a.cfg.AgentID, "case": caseName}, Points: throttlePts},
			&report.MetricsSeries{Name: "actual_qps", Labels: map[string]string{"agent": a.cfg.AgentID, "case": caseName}, Points: actualQPSPts},
		)
		if err := a.writer.WriteMetrics(reportID, metricsData); err != nil {
			log.Printf("[Agent] partial flush metrics error: %v", err)
		}
	}
}

// checkCancelled 向 Master 查询任务是否已被取消。
func (a *Agent) checkCancelled(reportID string) bool {
	u, _ := url.Parse(a.cfg.MasterAddr + "/api/agent/tasks/cancel")
	q := u.Query()
	q.Set("report_id", reportID)
	u.RawQuery = q.Encode()

	resp, err := a.client.Get(u.String())
	if err != nil {
		return false // 网络错误不当作取消
	}
	defer resp.Body.Close()

	var result struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	return result.Cancelled
}

// postResult 向 Master 上报任务结果
func (a *Agent) postResult(result robot_case.AgentTaskResult) error {
	body, _ := json.Marshal(result)
	resp, err := a.client.Post(a.cfg.MasterAddr+"/api/agent/tasks/result", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("master result endpoint returned %d", resp.StatusCode)
	}
	return nil
}

// registerToMaster 向 Master 注册本 Agent（仅上报 ID 和组信息，无需监听地址）
func (a *Agent) registerToMaster() error {
	if a.cfg.MasterAddr == "" {
		return nil
	}
	payload := map[string]string{"id": a.cfg.AgentID}
	if a.cfg.GroupID != "" {
		payload["group_id"] = a.cfg.GroupID
	}
	body, _ := json.Marshal(payload)
	resp, err := a.client.Post(a.cfg.MasterAddr+"/api/agents/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("master returned %d", resp.StatusCode)
	}
	log.Printf("[Agent] Registered to master: %s", a.cfg.MasterAddr)
	return nil
}

// heartbeatLoop 每 10 秒重新注册一次以维持 last_seen 心跳
func (a *Agent) heartbeatLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		_ = a.registerToMaster()
	}
}

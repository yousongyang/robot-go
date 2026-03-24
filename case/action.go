package atsf4g_go_robot_case

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lu "github.com/atframework/atframe-utils-go/lang_utility"
	log "github.com/atframework/atframe-utils-go/log"
	base "github.com/atframework/robot-go/base"
	report "github.com/atframework/robot-go/report"
	utils "github.com/atframework/robot-go/utils"
)

type CaseFunc func(*TaskActionCase, string, []string) error

var CaseActionActor sync.Map

type TaskActionCase struct {
	base.TaskActionBase
	Fn         CaseFunc
	logHandler func(openId string, format string, a ...any)
	OpenId     string
	Args       []string
}

func (t *TaskActionCase) BeforeYield() {
	channel, _ := CaseActionActor.Load(t.OpenId)
	channel.(chan struct{}) <- struct{}{}
}

func (t *TaskActionCase) AfterYield() {
	channel, ok := CaseActionActor.Load(t.OpenId)
	if !ok {
		newChannel := make(chan struct{}, 1)
		newChannel <- struct{}{}
		channel, _ = CaseActionActor.LoadOrStore(t.OpenId, newChannel)
	}
	<-channel.(chan struct{})
}

func (t *TaskActionCase) HookRun() error {
	t.AfterYield()
	defer t.BeforeYield()
	return t.Fn(t, t.OpenId, t.Args)
}

func (t *TaskActionCase) Log(format string, a ...any) {
	t.logHandler(t.OpenId, format, a...)
}

func init() {
	var _ base.TaskActionImpl = &TaskActionCase{}
	utils.RegisterCommand([]string{"run-case"}, CmdRunCase, "<case name> <openid-prefix> <user-count> <batch-count> <run-time> <args>", "运行用例", AutoCompleteCaseName, 0)
	utils.RegisterCommand([]string{"run-case-file"}, CmdRunCaseFile, "<file> <repeated_time>", "运行用例文件", AutoCompleteCaseName, 0)
	utils.RegisterCommand([]string{"run-case-stress"}, CmdRunCaseStress,
		"<caseName> <errorBreak> <openIdPrefix> <idStart> <idEnd> <batchCount> <targetQPS> <runTime> [args...]",
		"运行压测用例", AutoCompleteCaseName, 0)
}

type CaseAction struct {
	fun     CaseFunc
	timeout time.Duration
}

var caseMapContainer = make(map[string]CaseAction)

func RegisterCase(name string, fn CaseFunc, timeout time.Duration) {
	caseMapContainer[name] = CaseAction{
		fun:     fn,
		timeout: timeout,
	}
}

func AutoCompleteCaseName(string) []string {
	var res []string
	for k := range caseMapContainer {
		res = append(res, k)
	}
	return res
}

var (
	ProgressBarTotalCount   int64
	ProgressBarCurrentCount atomic.Int64

	FailedCount      atomic.Int64
	TotalFailedCount atomic.Int64
	RefreshFunc      *time.Timer
)

func RefreshProgressBar() {
	countProgressBar := ""
	width := 25
	var progress float64 = 0
	if ProgressBarTotalCount != 0 {
		progress = float64(ProgressBarCurrentCount.Load()) / float64(ProgressBarTotalCount)
		completed := int(progress * float64(width))
		countProgressBar = fmt.Sprintf("[%-*s] %d/%d", width, strings.Repeat("#", completed), ProgressBarCurrentCount.Load(), ProgressBarTotalCount)
		utils.StdoutLog(fmt.Sprintf("Total:%s || Failed:%d             ", countProgressBar, FailedCount.Load()))
		if ProgressBarCurrentCount.Load() >= ProgressBarTotalCount {
			return
		}
	}
	RefreshFunc = time.AfterFunc(time.Second, func() { RefreshProgressBar() })
}

func ClearProgressBar() {
	ProgressBarTotalCount = 0
	ProgressBarCurrentCount.Store(0)
	FailedCount.Store(0)
}

func InitProgressBar(totalCount int64) {
	ProgressBarTotalCount += totalCount
}

func AddProgressBarCount() {
	ProgressBarCurrentCount.Add(1)
}

func RunCaseWait(pendingCase []chan string) error {
	if len(pendingCase) == 0 {
		return nil
	}
	RefreshProgressBar()
	for _, ch := range pendingCase {
		result := <-ch
		if result != "" {
			return fmt.Errorf("Run Case Failed: %s", result)
		}
	}
	RefreshProgressBar()
	if RefreshFunc != nil {
		RefreshFunc.Stop()
		RefreshFunc = nil
	}
	ClearProgressBar()
	return nil
}

func RunCaseFile(caseFile string, repeatedTime int32) error {
	file, err := os.Open(caseFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// 检测 #!stress 头部，判断是否为压测模式
	isStressMode := false
	headerScanner := bufio.NewScanner(file)
	for headerScanner.Scan() {
		raw := strings.TrimSpace(headerScanner.Text())
		if raw == "" {
			continue
		}
		if raw == "#!stress" {
			isStressMode = true
		}
		break
	}

	beginTime := time.Now()
	mgr := report.GetGlobalManager()
	var tracer report.Tracer
	var pressure report.PressureController
	if mgr != nil {
		tracer = mgr.Tracer
		pressure = mgr.Pressure
	}

	for index := int32(0); index < repeatedTime; index++ {
		utils.StdoutLog(fmt.Sprintf("Run Case File: %s, Repeated Time: %d/%d, StressMode: %v", caseFile, index+1, repeatedTime, isStressMode))
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		var caseIndex int32 = 0
		var pendingCase []chan string
		for scanner.Scan() {
			line := scanner.Text()
			if idx := strings.Index(line, "#"); idx >= 0 {
				line = line[:idx]
			}
			line = strings.TrimSpace(line)
			if len(line) == 0 {
				continue
			}

			args := strings.Fields(line)
			if len(args) == 0 {
				continue
			}

			batchPending := false
			if strings.ToLower(args[len(args)-1]) == "&" {
				args = args[:len(args)-1]
				batchPending = true
			}

			if len(args) == 0 {
				continue
			}

			caseIndex++
			currentCaseIndex := caseIndex

			if isStressMode {
				// 压测模式: CaseName ErrorBreak openIdPrefix idStart idEnd batchCount targetQPS runTime [args...]
				waitingChan := make(chan string, 1)
				lineArgs := args
				go func() {
					params, parseErr := parseStressLine(lineArgs)
					if parseErr != "" {
						waitingChan <- fmt.Sprintf("Case[%d] parse error: %s", currentCaseIndex, parseErr)
						return
					}
					waitingChan <- RunCaseStress(params, tracer, pressure)
				}()
				pendingCase = append(pendingCase, waitingChan)
			} else {
				// 普通模式
				waitingChan := make(chan string, 1)
				lineArgs := args
				go func() {
					waitingChan <- RunCase(nil, lineArgs, currentCaseIndex, beginTime)
				}()
				pendingCase = append(pendingCase, waitingChan)
			}

			if batchPending {
				continue
			} else {
				err = RunCaseWait(pendingCase)
				if err != nil {
					return err
				}
				pendingCase = pendingCase[:0]
			}
		}

		err = RunCaseWait(pendingCase)
		if err != nil {
			return err
		}

		if err := scanner.Err(); err != nil {
			return err
		}
	}

	return nil
}

func RunCase(_ base.TaskActionImpl, cmd []string, caseIndex int32, beginTime time.Time) string {
	if len(cmd) < 5 {
		return "Args Error"
	}

	caseName := cmd[0]
	caseAction, ok := caseMapContainer[caseName]
	if !ok {
		return "Case Not Found"
	}

	openIdPrefix := cmd[1]
	if openIdPrefix == "" {
		return "OpenId Prefix Empty"
	}

	userCount, err := strconv.ParseInt(cmd[2], 10, 32)
	if err != nil {
		return err.Error()
	}

	batchCount, err := strconv.ParseInt(cmd[3], 10, 32)
	if err != nil {
		return err.Error()
	}
	if batchCount <= 0 {
		return "Batch Count Must Greater Than 0"
	}
	if batchCount > userCount {
		batchCount = userCount
	}

	runTime, err := strconv.ParseInt(cmd[4], 10, 32)
	if err != nil {
		return err.Error()
	}

	totalCount := atomic.Int64{}
	totalCount.Store(userCount * runTime)

	timeCounter := sync.Map{}
	openidChannel := make(chan string, userCount)
	for i := int64(0); i < userCount; i++ {
		// 初始化Time统计
		openId := openIdPrefix + strconv.FormatInt(i, 10)
		timeCounter.Store(openId, int32(runTime))
		// 初始化OpenId令牌
		openidChannel <- openId
	}

	InitProgressBar(totalCount.Load())

	caseActionChannel := make(chan *TaskActionCase, batchCount) // 限制并发数

	bufferWriter, _ := log.NewLogBufferedRotatingWriter(nil,
		fmt.Sprintf("../log/%d.%s.%s.%%N.log", caseIndex, caseName, beginTime.Format("15.04.05")), "", 50*1024*1024, 3, time.Second*3, 0)
	logHandler := func(openId string, format string, a ...any) {
		logString := fmt.Sprintf("[%s][%s]: %s", time.Now().Format("2006-01-02 15:04:05.000"), openId, fmt.Sprintf(format, a...))
		bufferWriter.Write(lu.StringtoBytes(logString))
	}
	defer func() {
		bufferWriter.Close()
		bufferWriter.AwaitClose()
	}()
	logHandler("System", "Case[%s] Start Running, Total Count: %d, Batch Count: %d, Run Time: %d", caseName, totalCount.Load(), batchCount, runTime)

	// Report 集成
	reportMgr := report.GetGlobalManager()
	var tracer report.Tracer
	if reportMgr != nil {
		tracer = reportMgr.Tracer
	}

	for i := int64(0); i < batchCount; i++ {
		// 创建TaskActionCase
		task := &TaskActionCase{
			TaskActionBase: *base.NewTaskActionBase(caseAction.timeout, "Case Runner"),
			Fn:             caseAction.fun,
			logHandler:     logHandler,
		}
		if len(cmd) > 5 {
			task.Args = cmd[5:]
		}
		task.TaskActionBase.Impl = task
		caseActionChannel <- task
		task.InitOnFinish(func(err error) {
			openId := task.OpenId
			currentCount, _ := timeCounter.Load(openId)
			currentCountInt := currentCount.(int32)
			timeCounter.Store(openId, currentCountInt-1)
			if currentCountInt-1 > 0 {
				// 还有运行次数，继续放回OpenId
				openidChannel <- openId
			}
			AddProgressBarCount()
			if err != nil {
				FailedCount.Add(1)
				TotalFailedCount.Add(1)
				task.Log("Run Case[%s] Failed: %v", task.Name, err)
			}
			caseActionChannel <- task
		})
	}

	mgr := base.NewTaskActionManager()
	finishChannel := make(chan struct{})
	go func() {
		successCount := int64(0)
		for action := range caseActionChannel {
			// 取出OpenId
			openId := <-openidChannel
			action.OpenId = openId

			// 打点: 先重置 Fn 再包装，防止任务复用时闭包嵌套累积
			action.Fn = caseAction.fun
			if tracer != nil {
				entry := tracer.NewEntry(caseName).Start()
				origFn := action.Fn
				action.Fn = func(t *TaskActionCase, oid string, args []string) error {
					err := origFn(t, oid, args)
					entry.EndWithError(err)
					return err
				}
			}

			// 运行TaskAction
			mgr.RunTaskAction(action)
			successCount++
			if successCount >= totalCount.Load() {
				break
			}
		}
		// 等待任务完成
		mgr.WaitAll()
		finishChannel <- struct{}{}
	}()
	<-finishChannel
	useTime := time.Since(beginTime).String()
	logHandler("System", "Case[%s] All Completed, Total Time: %s", caseName, useTime)

	if TotalFailedCount.Load() != 0 {
		return fmt.Sprintf("Complete With %d Failed Index:[%d] Args: %v, Total Time: %s", TotalFailedCount.Load(), caseIndex, cmd, useTime)
	}
	utils.StdoutLog(fmt.Sprintf("Complete All Success Index:[%d] Args: %v, Total Time: %s", caseIndex, cmd, useTime))
	return ""
}

func CmdRunCase(task base.TaskActionImpl, cmd []string) string {
	return RunCase(task, cmd, 0, time.Now())
}

func CmdRunCaseFile(task base.TaskActionImpl, cmd []string) string {
	if len(cmd) < 2 {
		return "Args Error"
	}
	repeatedTime, err := strconv.ParseInt(cmd[1], 10, 32)
	if err != nil {
		return err.Error()
	}

	if runErr := RunCaseFile(cmd[0], int32(repeatedTime)); runErr != nil {
		return runErr.Error()
	}
	return ""
}

// parseStressLine 解析压测模式的一行参数
// 格式: CaseName ErrorBreak openIdPrefix idStart idEnd batchCount targetQPS runTime [args...]
func parseStressLine(cmd []string) (StressParams, string) {
	if len(cmd) < 8 {
		return StressParams{}, "Args Error: need at least 8 args (CaseName ErrorBreak openIdPrefix idStart idEnd batchCount targetQPS runTime)"
	}
	errorBreak := strings.ToLower(cmd[1]) == "true" || cmd[1] == "1"
	idStart, err := strconv.ParseInt(cmd[3], 10, 64)
	if err != nil {
		return StressParams{}, "idStart parse error: " + err.Error()
	}
	idEnd, err := strconv.ParseInt(cmd[4], 10, 64)
	if err != nil {
		return StressParams{}, "idEnd parse error: " + err.Error()
	}
	batchCount, err := strconv.ParseInt(cmd[5], 10, 64)
	if err != nil {
		return StressParams{}, "batchCount parse error: " + err.Error()
	}
	targetQPS, err := strconv.ParseFloat(cmd[6], 64)
	if err != nil {
		return StressParams{}, "targetQPS parse error: " + err.Error()
	}
	runTime, err := strconv.ParseInt(cmd[7], 10, 64)
	if err != nil {
		return StressParams{}, "runTime parse error: " + err.Error()
	}
	params := StressParams{
		CaseName:     cmd[0],
		ErrorBreak:   errorBreak,
		OpenIDPrefix: cmd[2],
		OpenIDStart:  idStart,
		OpenIDEnd:    idEnd,
		BatchCount:   batchCount,
		TargetQPS:    targetQPS,
		RunTime:      runTime,
	}
	if len(cmd) > 8 {
		params.ExtraArgs = cmd[8:]
	}
	return params, ""
}

// ParseStressLine 是 parseStressLine 的导出版本，供 master 包解析 case 文件行。
func ParseStressLine(cmd []string) (StressParams, string) {
	return parseStressLine(cmd)
}

// CmdRunCaseStress 压测模式命令入口
// 格式: <caseName> <errorBreak> <openIdPrefix> <idStart> <idEnd> <batchCount> <targetQPS> <runTime> [args...]
func CmdRunCaseStress(_ base.TaskActionImpl, cmd []string) string {
	params, parseErr := parseStressLine(cmd)
	if parseErr != "" {
		return parseErr
	}
	mgr := report.GetGlobalManager()
	var tracer report.Tracer
	var pressure report.PressureController
	if mgr != nil {
		tracer = mgr.Tracer
		pressure = mgr.Pressure
	}
	return RunCaseStress(params, tracer, pressure)
}

// RunCaseStress 压测模式执行，支持 QPS 均匀控制与账号 ID 范围。
// tracer / pressure 为 nil 时退化为无报表的纯执行模式。
// params.ErrorBreak 为 true 时遇到第一个错误即停止调度。
func RunCaseStress(
	params StressParams,
	tracer report.Tracer,
	pressure report.PressureController,
) string {
	return RunCaseStressWithContext(context.Background(), params, tracer, pressure)
}

// RunCaseStressWithContext 与 RunCaseStress 相同，但支持通过 context 取消执行。
func RunCaseStressWithContext(
	ctx context.Context,
	params StressParams,
	tracer report.Tracer,
	pressure report.PressureController,
) string {
	caseName := params.CaseName
	caseAction, ok := caseMapContainer[caseName]
	if !ok {
		return "Case Not Found"
	}

	userCount := params.UserCount()
	if userCount <= 0 {
		return "ID range is empty"
	}

	batchCount := params.BatchCount
	if batchCount <= 0 {
		batchCount = 1
	}
	if batchCount > userCount {
		batchCount = userCount
	}

	runTime := params.RunTime
	if runTime <= 0 {
		runTime = 1
	}

	beginTime := time.Now()
	totalCount := userCount * runTime

	qpsCtrl := NewQPSController(params.TargetQPS)

	// 如果有 pressure controller，将 qps 控制与压力检测联动
	if pressure != nil {
		pressure.SetTargetQPS(params.TargetQPS)
		pressure.Start(time.Second)
		defer pressure.Stop()
	}

	// 构造账号池
	openidChan := make(chan string, userCount)
	timeCounter := sync.Map{}
	for i := params.OpenIDStart; i < params.OpenIDEnd; i++ {
		openId := params.OpenIDPrefix + strconv.FormatInt(i, 10)
		timeCounter.Store(openId, int32(runTime))
		openidChan <- openId
	}

	InitProgressBar(totalCount)

	bufferWriter, _ := log.NewLogBufferedRotatingWriter(nil,
		fmt.Sprintf("../log/stress.%s.%s.%%N.log", caseName, beginTime.Format("15.04.05")), "", 50*1024*1024, 3, time.Second*3, 0)
	logHandler := func(openId string, format string, a ...any) {
		logString := fmt.Sprintf("[%s][%s]: %s", time.Now().Format("2006-01-02 15:04:05.000"), openId, fmt.Sprintf(format, a...))
		bufferWriter.Write(lu.StringtoBytes(logString))
	}
	defer func() {
		bufferWriter.Close()
		bufferWriter.AwaitClose()
	}()

	logHandler("System", "StressCase[%s] Start, Users: %d, Batch: %d, QPS: %.1f, RunTime: %d, ErrorBreak: %v",
		caseName, userCount, batchCount, params.TargetQPS, runTime, params.ErrorBreak)

	caseActionChannel := make(chan *TaskActionCase, batchCount)
	var stressFailedCount atomic.Int64
	var errorBreakTriggered atomic.Bool

	for i := int64(0); i < batchCount; i++ {
		task := &TaskActionCase{
			TaskActionBase: *base.NewTaskActionBase(caseAction.timeout, "Stress Runner"),
			Fn:             caseAction.fun,
			logHandler:     logHandler,
		}
		if len(params.ExtraArgs) > 0 {
			task.Args = params.ExtraArgs
		}
		task.TaskActionBase.Impl = task
		caseActionChannel <- task
		task.InitOnFinish(func(err error) {
			openId := task.OpenId
			current, _ := timeCounter.Load(openId)
			currentInt := current.(int32)
			timeCounter.Store(openId, currentInt-1)
			if currentInt-1 > 0 {
				openidChan <- openId
			}
			AddProgressBarCount()
			if err != nil {
				stressFailedCount.Add(1)
				FailedCount.Add(1)
				TotalFailedCount.Add(1)
				task.Log("StressCase[%s] Failed: %v", caseName, err)
				if params.ErrorBreak {
					errorBreakTriggered.Store(true)
				}
			}
			if pressure != nil {
				pressure.DonePending()
			}
			caseActionChannel <- task
		})
	}

	mgr := base.NewTaskActionManager()
	finishChannel := make(chan struct{})
	go func() {
		successCount := int64(0)
		for action := range caseActionChannel {
			// ErrorBreak: 遇到错误时停止调度新任务
			if errorBreakTriggered.Load() {
				caseActionChannel <- action
				break
			}
			// Context 取消：外部请求停止
			if ctx.Err() != nil {
				caseActionChannel <- action
				break
			}

			openId := <-openidChan
			action.OpenId = openId

			// QPS 控制: 联动 pressure
			if pressure != nil && params.TargetQPS > 0 {
				effective := pressure.EffectiveQPS()
				qpsCtrl.SetQPS(math.Max(effective, 1))
			}
			qpsCtrl.Acquire()

			if pressure != nil {
				pressure.AddPending()
			}

			// 打点: 先重置 Fn 再包装，防止任务复用时闭包嵌套累积
			action.Fn = caseAction.fun
			if tracer != nil {
				entry := tracer.NewEntry(caseName).Start()
				origFn := action.Fn
				action.Fn = func(t *TaskActionCase, oid string, args []string) error {
					err := origFn(t, oid, args)
					entry.EndWithError(err)
					return err
				}
			}

			mgr.RunTaskAction(action)
			successCount++
			if successCount >= totalCount {
				break
			}
		}
		mgr.WaitAll()
		finishChannel <- struct{}{}
	}()
	<-finishChannel

	useTime := time.Since(beginTime).String()
	logHandler("System", "StressCase[%s] Completed, Total Time: %s", caseName, useTime)

	if ctx.Err() != nil {
		return fmt.Sprintf("StressCase[%s] Cancelled, Total Time: %s", caseName, useTime)
	}

	if stressFailedCount.Load() != 0 {
		return fmt.Sprintf("StressCase[%s] Complete With %d Failed, Total Time: %s", caseName, stressFailedCount.Load(), useTime)
	}
	utils.StdoutLog(fmt.Sprintf("StressCase[%s] All Success, Total Time: %s", caseName, useTime))
	return ""
}

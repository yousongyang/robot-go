package report

import "time"

// PressureLevel 压力等级
type PressureLevel int

const (
	PressureLevelNormal   PressureLevel = 0 // 正常
	PressureLevelWarning  PressureLevel = 1 // 警告（降速 10%）
	PressureLevelHigh     PressureLevel = 2 // 高压（降速 40%）
	PressureLevelCritical PressureLevel = 3 // 临界（降至最低安全 QPS）
)

// PressureSnapshot 某一时刻的压力快照
type PressureSnapshot struct {
	Timestamp      time.Time     `json:"timestamp"`
	Level          PressureLevel `json:"level"`
	GoroutineCount int           `json:"goroutine_count"`
	HeapAllocMB    float64       `json:"heap_alloc_mb"`
	PendingReqs    int64         `json:"pending_requests"`
	TargetQPS      float64       `json:"target_qps"`
	ActualQPS      float64       `json:"actual_qps"`
	ThrottleRatio  float64       `json:"throttle_ratio"` // 1.0=不限速，0.2=最低
}

// PressureController 自压力检测与 QPS 自适应控制器
type PressureController interface {
	// SetTargetQPS 设置目标 QPS
	SetTargetQPS(qps float64)
	// EffectiveQPS 返回当前受压力调节后的允许 QPS
	EffectiveQPS() float64
	// AddPending 请求开始，增加待处理计数
	AddPending()
	// DonePending 请求完成，减少待处理计数
	DonePending()
	// Start 启动后台检测
	Start(interval time.Duration)
	// Stop 停止检测
	Stop()
	// CurrentLevel 返回当前压力等级
	CurrentLevel() PressureLevel
	// Snapshots 返回全部快照
	Snapshots() []PressureSnapshot
}

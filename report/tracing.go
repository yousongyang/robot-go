package report

// TracingCode 打点结果码，0 表示成功，非 0 表示失败
type TracingCode = int32

const TracingSuccess TracingCode = 0

// TracingRecord 聚合数据
type TracingRecord struct {
	Timestamp int64  `json:"timestamp"`
	StartData bool   `json:"startRecord,omitempty"` // true=开始记录；false(默认)=结束记录，不输出
	Name      string `json:"name"`
	Count     int    `json:"count"` // 代表的请求数
	// Start 数据
	// End 数据
	TotalDurationMs int64 `json:"durationMs,omitempty"`
	MinDurationMs   int64 `json:"minDurationMs,omitempty"`
	MaxDurationMs   int64 `json:"maxDurationMs,omitempty"`
	Variance        int64 `json:"variance,omitempty"` // 时间方差
	// Code 统计聚合
	Code map[int]int `json:"Code,omitempty"`
	// 错误统计聚合
	Error map[string]int `json:"Error,omitempty"`
}

// TracingEntry 代表一次正在进行的打点操作
type TracingEntry interface {
	// Start 开始计时，支持链式调用
	Start() TracingEntry
	// End 结束计时并记录结果码
	End(code TracingCode, err string)
	// EndWithError 结束计时，非 nil error 视为失败（code = -1）
	EndWithError(err error)
}

// Tracer 管理打点的生命周期和数据收集
type Tracer interface {
	// NewEntry 创建一个新的打点实例（尚未开始计时）
	NewEntry(name string) TracingEntry
	// Flush 返回快照并清空内部缓冲
	Flush() []*TracingRecord
	// Reset 清空所有数据
	Reset()
}

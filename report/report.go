package report

import "time"

// ReportMeta 报表元数据
type ReportMeta struct {
	ReportID  string    `json:"report_id"`
	Title     string    `json:"title"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	AgentIDs  []string  `json:"agent_ids"`
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt 自动过期时间；零值表示永不过期
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// RawDataSize 原始打点+指标数据 JSON 序列化大小（字节），近似 Redis 占用
	RawDataSize int64 `json:"raw_data_size,omitempty"`
	// ReportSize 生成的 HTML 报告文件大小（字节）；首次生成后更新
	ReportSize int64 `json:"report_size,omitempty"`
}

// ReportData 一次报表的完整原始数据
type ReportData struct {
	Meta     ReportMeta       `json:"meta"`
	Tracings []*TracingRecord `json:"tracings"`
	Metrics  []*MetricsSeries `json:"metrics"`
}

// ReportWriter 负责将数据写入存储
type ReportWriter interface {
	// WriteTracings 追加写入打点数据
	WriteTracings(reportID string, records []*TracingRecord) error
	// WriteMetrics 追加写入指标数据
	WriteMetrics(reportID string, series []*MetricsSeries) error
	// WriteMeta 写入/更新报表元数据
	WriteMeta(meta *ReportMeta) error
	// Close 刷盘/关闭
	Close() error
}

// ReportReader 负责从存储读取数据
type ReportReader interface {
	// ReadReport 读取完整报表数据
	ReadReport(reportID string) (*ReportData, error)
	// ListReports 列出所有报表摘要
	ListReports() ([]*ReportMeta, error)
}

// HTMLGenerator 将报表数据渲染为 HTML
type HTMLGenerator interface {
	// Generate 根据报表数据生成 HTML 字节
	Generate(data *ReportData) ([]byte, error)
	// GenerateToFile 生成 HTML 并写入文件
	GenerateToFile(data *ReportData, outputPath string) error
}

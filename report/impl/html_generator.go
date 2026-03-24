package impl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"os"
	"sort"
	"time"

	"github.com/atframework/robot-go/report"
)

// EChartsHTMLGenerator 使用 ECharts CDN 生成独立 HTML 报告
type EChartsHTMLGenerator struct{}

func NewEChartsHTMLGenerator() *EChartsHTMLGenerator {
	return &EChartsHTMLGenerator{}
}

func (g *EChartsHTMLGenerator) Generate(data *report.ReportData) ([]byte, error) {
	td := g.buildTemplateData(data)
	tmpl, err := template.New("report").Parse(htmlTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, td); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

func (g *EChartsHTMLGenerator) GenerateToFile(data *report.ReportData, outputPath string) error {
	html, err := g.Generate(data)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, html, 0640)
}

// --- 模板数据结构 ---

type caseStat struct {
	Name    string  `json:"name"`
	Total   int     `json:"total"`
	Success int     `json:"success"`
	Failed  int     `json:"failed"`
	AvgMs   float64 `json:"avgMs"`
	P50Ms   int64   `json:"p50Ms"`
	P90Ms   int64   `json:"p90Ms"`
	P99Ms   int64   `json:"p99Ms"`
	MinMs   int64   `json:"minMs"`
	MaxMs   int64   `json:"maxMs"`
}

type metricsSection struct {
	CaseGroup  string
	ShowHeader bool
	Name       string
	Labels     string
	Times      template.JS
	Values     template.JS
}

// metricsDataEntry 为指标下拉筛选提供的 JS 可用数据
type metricsDataEntry struct {
	Name      string    `json:"name"`
	CaseGroup string    `json:"caseGroup"`
	Labels    string    `json:"labels"`
	Times     []string  `json:"times"`
	Values    []float64 `json:"values"`
}

// chartSeriesData 按秒聚合的单条 case 时间序列
type chartSeriesData struct {
	Name    string           `json:"name"`
	QPS     []int            `json:"qps"`
	Success []int            `json:"success"`
	Failed  []int            `json:"failed"`
	AvgMs   []float64        `json:"avgMs"`
	P50Ms   []int64          `json:"p50Ms"`
	P90Ms   []int64          `json:"p90Ms"`
	P99Ms   []int64          `json:"p99Ms"`
	Errors  []errorCodeEntry `json:"errors"`
}

type chartData struct {
	TimeLabels []string          `json:"timeLabels"`
	Series     []chartSeriesData `json:"series"`
}

type errorCodeEntry struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

type templateData struct {
	Title     string
	ReportID  string
	StartTime string
	EndTime   string
	CreatedAt string
	// 全局汇总
	TotalReqs   int
	SuccessReqs int
	FailedReqs  int
	AvgMs       float64
	P50Ms       float64
	P90Ms       float64
	P99Ms       float64
	// 分 Case 统计
	CaseStats []caseStat
	// JSON（供 JS 读取）
	ChartDataJSON  template.JS
	ErrorCodesJSON template.JS
	// Metrics
	OnlineUsersJSON template.JS      // 在线用户多系列 JSON，显示在 QPS 图前
	MetricsSections []metricsSection // 其他指标（仅用于判断是否有非 online 指标）
	MetricsJSON     template.JS      // 全部非 online_users 指标的 JSON，供 JS 动态渲染
}

func (g *EChartsHTMLGenerator) buildTemplateData(data *report.ReportData) *templateData {
	td := &templateData{
		Title:           data.Meta.Title,
		ReportID:        data.Meta.ReportID,
		StartTime:       data.Meta.StartTime.Format("2006-01-02 15:04:05"),
		EndTime:         data.Meta.EndTime.Format("2006-01-02 15:04:05"),
		CreatedAt:       data.Meta.CreatedAt.Format("2006-01-02 15:04:05"),
		ChartDataJSON:   template.JS(`{"timeLabels":[],"series":[]}`),
		ErrorCodesJSON:  template.JS(`[]`),
		MetricsJSON:     template.JS(`null`),
		OnlineUsersJSON: template.JS(`null`),
	}

	if len(data.Tracings) > 0 {
		g.processTracings(td, data.Tracings)
	}
	if len(data.Metrics) > 0 {
		g.processMetrics(td, data.Metrics)
	}
	return td
}

func (g *EChartsHTMLGenerator) processTracings(td *templateData, records []*report.TracingRecord) {
	td.TotalReqs = len(records)

	// --- 按 case name 分组（保持出现顺序）---
	caseOrder := make([]string, 0)
	caseRecords := make(map[string][]*report.TracingRecord)
	for _, r := range records {
		if _, exists := caseRecords[r.Name]; !exists {
			caseOrder = append(caseOrder, r.Name)
		}
		caseRecords[r.Name] = append(caseRecords[r.Name], r)
	}

	// --- 全局统计 ---
	allDurations := make([]int64, 0, len(records))
	globalErrorCodes := make(map[int32]int)
	var totalMs int64
	for _, r := range records {
		totalMs += r.DurationMs
		allDurations = append(allDurations, r.DurationMs)
		if r.Code == report.TracingSuccess {
			td.SuccessReqs++
		} else {
			td.FailedReqs++
			globalErrorCodes[r.Code]++
		}
	}
	if td.TotalReqs > 0 {
		td.AvgMs = float64(totalMs) / float64(td.TotalReqs)
		sort.Slice(allDurations, func(i, j int) bool { return allDurations[i] < allDurations[j] })
		td.P50Ms = float64(percentile(allDurations, 50))
		td.P90Ms = float64(percentile(allDurations, 90))
		td.P99Ms = float64(percentile(allDurations, 99))
	}

	// --- 分 Case 统计 ---
	for _, name := range caseOrder {
		recs := caseRecords[name]
		cs := caseStat{Name: name, Total: len(recs)}
		var csTotal int64
		durs := make([]int64, 0, len(recs))
		for _, r := range recs {
			csTotal += r.DurationMs
			durs = append(durs, r.DurationMs)
			if r.Code == report.TracingSuccess {
				cs.Success++
			} else {
				cs.Failed++
			}
		}
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		if len(durs) > 0 {
			cs.AvgMs = math.Round(float64(csTotal)/float64(len(durs))*10) / 10
			cs.P50Ms = percentile(durs, 50)
			cs.P90Ms = percentile(durs, 90)
			cs.P99Ms = percentile(durs, 99)
			cs.MinMs = durs[0]
			cs.MaxMs = durs[len(durs)-1]
		}
		td.CaseStats = append(td.CaseStats, cs)
	}

	// --- 按秒聚合（每个 case 独立 bucket）---
	type bucket struct {
		success int
		failed  int
		totalMs int64
		count   int
		durs    []int64
	}
	allSecs := make(map[int64]bool)
	caseBuckets := make(map[string]map[int64]*bucket)
	for _, name := range caseOrder {
		caseBuckets[name] = make(map[int64]*bucket)
		for _, r := range caseRecords[name] {
			sec := r.EndTime.Unix()
			allSecs[sec] = true
			b := caseBuckets[name][sec]
			if b == nil {
				b = &bucket{}
				caseBuckets[name][sec] = b
			}
			b.count++
			b.totalMs += r.DurationMs
			b.durs = append(b.durs, r.DurationMs)
			if r.Code == report.TracingSuccess {
				b.success++
			} else {
				b.failed++
			}
		}
	}

	secs := make([]int64, 0, len(allSecs))
	for s := range allSecs {
		secs = append(secs, s)
	}
	sort.Slice(secs, func(i, j int) bool { return secs[i] < secs[j] })

	cd := chartData{TimeLabels: make([]string, len(secs))}
	for i, sec := range secs {
		cd.TimeLabels[i] = time.Unix(sec, 0).Format("15:04:05")
	}
	for _, name := range caseOrder {
		sd := chartSeriesData{
			Name:    name,
			QPS:     make([]int, len(secs)),
			Success: make([]int, len(secs)),
			Failed:  make([]int, len(secs)),
			AvgMs:   make([]float64, len(secs)),
			P50Ms:   make([]int64, len(secs)),
			P90Ms:   make([]int64, len(secs)),
			P99Ms:   make([]int64, len(secs)),
		}
		bm := caseBuckets[name]
		for i, sec := range secs {
			b := bm[sec]
			if b == nil {
				continue
			}
			sd.QPS[i] = b.count
			sd.Success[i] = b.success
			sd.Failed[i] = b.failed
			if b.count > 0 {
				sd.AvgMs[i] = math.Round(float64(b.totalMs)/float64(b.count)*10) / 10
			}
			sort.Slice(b.durs, func(a, c int) bool { return b.durs[a] < b.durs[c] })
			sd.P50Ms[i] = percentile(b.durs, 50)
			sd.P90Ms[i] = percentile(b.durs, 90)
			sd.P99Ms[i] = percentile(b.durs, 99)
		}
		// 每个 case 独立的错误码统计（供 case 筛选下拉框过滤饼图）
		caseErrs := make(map[int32]int)
		for _, r := range caseRecords[name] {
			if r.Code != report.TracingSuccess {
				caseErrs[r.Code]++
			}
		}
		for code, cnt := range caseErrs {
			sd.Errors = append(sd.Errors, errorCodeEntry{Name: fmt.Sprintf("code_%d", code), Value: cnt})
		}
		cd.Series = append(cd.Series, sd)
	}

	cdJSON, _ := json.Marshal(cd)
	td.ChartDataJSON = template.JS(cdJSON)

	// --- 错误码 ---
	errorCodes := make([]errorCodeEntry, 0, len(globalErrorCodes))
	for code, cnt := range globalErrorCodes {
		errorCodes = append(errorCodes, errorCodeEntry{Name: fmt.Sprintf("code_%d", code), Value: cnt})
	}
	ecJSON, _ := json.Marshal(errorCodes)
	td.ErrorCodesJSON = template.JS(ecJSON)
}

func (g *EChartsHTMLGenerator) processMetrics(td *templateData, series []*report.MetricsSeries) {
	var onlineUsersSeries, otherSeries []*report.MetricsSeries
	for _, s := range series {
		if len(s.Points) == 0 {
			continue
		}
		if s.Name == "online_users" {
			onlineUsersSeries = append(onlineUsersSeries, s)
		} else {
			otherSeries = append(otherSeries, s)
		}
	}
	g.processOnlineUsers(td, onlineUsersSeries)
	// 其他指标：按 case+name 排序，构建 JSON
	sort.Slice(otherSeries, func(i, j int) bool {
		if otherSeries[i].Labels["case"] != otherSeries[j].Labels["case"] {
			return otherSeries[i].Labels["case"] < otherSeries[j].Labels["case"]
		}
		return otherSeries[i].Name < otherSeries[j].Name
	})
	entries := make([]metricsDataEntry, 0, len(otherSeries))
	for _, s := range otherSeries {
		times := make([]string, len(s.Points))
		values := make([]float64, len(s.Points))
		for i, pt := range s.Points {
			times[i] = pt.Timestamp.Format("15:04:05")
			values[i] = math.Round(pt.Value*100) / 100
		}
		labelsStr := ""
		for k, v := range s.Labels {
			if k == "case" {
				continue
			}
			if labelsStr != "" {
				labelsStr += ", "
			}
			labelsStr += k + "=" + v
		}
		entries = append(entries, metricsDataEntry{
			Name:      s.Name,
			CaseGroup: s.Labels["case"],
			Labels:    labelsStr,
			Times:     times,
			Values:    values,
		})
		td.MetricsSections = append(td.MetricsSections, metricsSection{}) // 只用于判断是否有数据
	}
	if len(entries) > 0 {
		mj, _ := json.Marshal(entries)
		td.MetricsJSON = template.JS(mj)
	}
}

// processOnlineUsers 将全部 online_users 系列合并成一张多系列折线图的 JSON。
// 每个 Agent 一条线（按 agent 标签区分），并附加一条汇总的 Total 线。
func (g *EChartsHTMLGenerator) processOnlineUsers(td *templateData, series []*report.MetricsSeries) {
	if len(series) == 0 {
		return
	}
	// 收集全部时间点
	timeSet := make(map[string]bool)
	for _, s := range series {
		for _, pt := range s.Points {
			timeSet[pt.Timestamp.Format("15:04:05")] = true
		}
	}
	timeLabelsSorted := make([]string, 0, len(timeSet))
	for t := range timeSet {
		timeLabelsSorted = append(timeLabelsSorted, t)
	}
	sort.Strings(timeLabelsSorted)
	timeIdx := make(map[string]int, len(timeLabelsSorted))
	for i, t := range timeLabelsSorted {
		timeIdx[t] = i
	}

	type ouEntry struct {
		Name   string    `json:"name"`
		Values []float64 `json:"values"`
	}
	type ouChartData struct {
		TimeLabels []string  `json:"timeLabels"`
		Series     []ouEntry `json:"series"`
	}
	cd := ouChartData{TimeLabels: timeLabelsSorted}
	total := make([]float64, len(timeLabelsSorted))
	for _, s := range series {
		values := make([]float64, len(timeLabelsSorted))
		for _, pt := range s.Points {
			t := pt.Timestamp.Format("15:04:05")
			if idx, ok := timeIdx[t]; ok {
				values[idx] = math.Round(pt.Value*100) / 100
			}
		}
		name := "online_users"
		if v, ok := s.Labels["agent"]; ok && v != "" {
			name = v
		}
		// 不附加 case 标签：online_users 是进程级指标
		cd.Series = append(cd.Series, ouEntry{Name: name, Values: values})
		for i, v := range values {
			total[i] += v
		}
	}
	// 多于一个 Agent 时才显示 Total
	if len(series) > 1 {
		rounded := make([]float64, len(total))
		for i, v := range total {
			rounded[i] = math.Round(v*100) / 100
		}
		cd.Series = append(cd.Series, ouEntry{Name: "Total", Values: rounded})
	}
	j, _ := json.Marshal(cd)
	td.OnlineUsersJSON = template.JS(j)
}

func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(p)/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

var _ report.HTMLGenerator = (*EChartsHTMLGenerator)(nil)

const htmlTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>{{.Title}} - 压测报告</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",sans-serif;background:#f0f2f5;color:#333}
.hdr{background:linear-gradient(135deg,#667eea 0%,#764ba2 100%);color:#fff;padding:28px 40px}
.hdr h1{font-size:22px;margin-bottom:6px}
.hdr .meta{font-size:13px;opacity:.85}
.wrap{max-width:1400px;margin:0 auto;padding:24px}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:14px;margin-bottom:24px}
.cd{background:#fff;border-radius:8px;padding:14px 18px;box-shadow:0 1px 3px rgba(0,0,0,.08);text-align:center}
.cd .lb{font-size:11px;color:#999;text-transform:uppercase;letter-spacing:.5px;margin-bottom:2px}
.cd .vl{font-size:26px;font-weight:700}
.cd .vl.ok{color:#52c41a}.cd .vl.fl{color:#f5222d}.cd .vl.inf{color:#1890ff}
.bx{background:#fff;border-radius:8px;padding:18px;margin-bottom:18px;box-shadow:0 1px 3px rgba(0,0,0,.08)}
.bx h3{font-size:14px;color:#555;margin-bottom:10px}
.ct{width:100%;height:400px}
table.st{width:100%;border-collapse:collapse;font-size:13px}
table.st th,table.st td{padding:9px 12px;text-align:right;border-bottom:1px solid #f0f0f0}
table.st th{background:#fafafa;font-weight:600;color:#666}
table.st th:first-child,table.st td:first-child{text-align:left}
table.st tr:hover{background:#f5f5ff}
.stit{font-size:16px;font-weight:600;color:#333;margin:24px 0 14px;padding-left:10px;border-left:3px solid #667eea}
</style>
</head>
<body>
<div class="hdr">
  <h1>{{.Title}}</h1>
  <div class="meta">Report ID: {{.ReportID}} &nbsp;|&nbsp; {{.StartTime}} — {{.EndTime}} &nbsp;|&nbsp; Generated: {{.CreatedAt}}</div>
</div>
<div class="wrap">
  <div class="cards">
    <div class="cd"><div class="lb">总请求</div><div class="vl inf">{{.TotalReqs}}</div></div>
    <div class="cd"><div class="lb">成功</div><div class="vl ok">{{.SuccessReqs}}</div></div>
    <div class="cd"><div class="lb">失败</div><div class="vl fl">{{.FailedReqs}}</div></div>
    <div class="cd"><div class="lb">Avg</div><div class="vl">{{printf "%.1f" .AvgMs}} ms</div></div>
    <div class="cd"><div class="lb">P50</div><div class="vl">{{printf "%.0f" .P50Ms}} ms</div></div>
    <div class="cd"><div class="lb">P90</div><div class="vl">{{printf "%.0f" .P90Ms}} ms</div></div>
    <div class="cd"><div class="lb">P99</div><div class="vl">{{printf "%.0f" .P99Ms}} ms</div></div>
  </div>

  {{if .CaseStats}}
  <div class="stit">分 Case 统计</div>
  <div class="bx">
    <table class="st">
      <thead><tr><th>Case</th><th>Total</th><th>Success</th><th>Failed</th><th>Avg(ms)</th><th>P50(ms)</th><th>P90(ms)</th><th>P99(ms)</th><th>Min(ms)</th><th>Max(ms)</th></tr></thead>
      <tbody>{{range .CaseStats}}
      <tr><td>{{.Name}}</td><td>{{.Total}}</td><td style="color:#52c41a">{{.Success}}</td><td style="color:#f5222d">{{.Failed}}</td><td>{{printf "%.1f" .AvgMs}}</td><td>{{.P50Ms}}</td><td>{{.P90Ms}}</td><td>{{.P99Ms}}</td><td>{{.MinMs}}</td><td>{{.MaxMs}}</td></tr>{{end}}
      </tbody>
    </table>
  </div>
  {{end}}

  <div class="bx" style="display:flex;align-items:center;gap:12px;padding:10px 18px">
    <label style="font-weight:600;font-size:13px;color:#555">选择 Case：</label>
    <select id="caseFilter" style="padding:6px 12px;border:1px solid #d9d9d9;border-radius:4px;font-size:13px;min-width:200px;cursor:pointer">
      <option value="">全部 Case</option>
    </select>
  </div>

  {{if .OnlineUsersJSON}}
  <div class="stit">在线用户 (Online Users, 点击图例切换显示)</div>
  <div class="bx"><div class="ct" id="c_online_users"></div></div>
  {{end}}

  <div class="stit">QPS 时间曲线 (点击图例切换显示)</div>
  <div class="bx"><div class="ct" id="c_qps"></div></div>

  <div class="stit">延迟分位数 P50 / P90 / P99 (点击图例切换显示)</div>
  <div class="bx"><div class="ct" id="c_lat"></div></div>

  <div class="stit">成功 / 失败趋势</div>
  <div class="bx"><div class="ct" id="c_sf"></div></div>

  <div class="stit">错误码分布</div>
  <div class="bx"><div class="ct" id="c_err" style="height:320px"></div></div>

  {{if .MetricsSections}}
  <div class="stit">指标 (Metrics)</div>
  <div class="bx" style="display:flex;align-items:center;gap:12px;padding:10px 18px">
    <label style="font-weight:600;font-size:13px;color:#555">选择指标：</label>
    <select id="metricFilter" style="padding:6px 12px;border:1px solid #d9d9d9;border-radius:4px;font-size:13px;min-width:200px;cursor:pointer">
      <option value="">全部指标</option>
    </select>
  </div>
  <div id="mc"></div>
  {{end}}
</div>

<script>
(function(){
var D={{.ChartDataJSON}};
var EC={{.ErrorCodesJSON}};
var C=['#5470c6','#91cc75','#fac858','#ee6666','#73c0de','#3ba272','#fc8452','#9a60b4','#ea7ccc'];
var charts={};

function mk(id,opt){
  var d=document.getElementById(id);if(!d)return null;
  if(charts[id]){charts[id].dispose();}
  var c=echarts.init(d);c.setOption(opt);
  window.addEventListener('resize',function(){c.resize();});
  charts[id]=c;
  return c;
}

// 初始化 Case 筛选下拉框
var sel=document.getElementById('caseFilter');
if(sel){
  D.series.forEach(function(s){
    var o=document.createElement('option');o.value=s.name;o.textContent=s.name;sel.appendChild(o);
  });
  sel.addEventListener('change',function(){renderCharts(sel.value);});
}

function filterSeries(caseName){
  if(!caseName)return D.series;
  return D.series.filter(function(s){return s.name===caseName;});
}

function renderCharts(caseName){
  var fs=filterSeries(caseName);

  // ---- QPS 曲线 ----
  var qS=[];
  fs.forEach(function(s){
    var i=D.series.indexOf(s);
    qS.push({name:s.name,type:'line',smooth:true,symbol:'circle',symbolSize:3,data:s.qps,
      itemStyle:{color:C[i%C.length]},emphasis:{focus:'series'}});
  });
  mk('c_qps',{
    tooltip:{trigger:'axis'},
    legend:{data:fs.map(function(s){return s.name;}),top:0,type:'scroll',selectedMode:'multiple'},
    grid:{top:40,bottom:35,left:55,right:20},
    xAxis:{type:'category',data:D.timeLabels,axisLabel:{rotate:30,fontSize:11}},
    yAxis:{type:'value',name:'req/s'},
    series:qS
  });

  // ---- 延迟 P50/P90/P99 ----
  var lS=[];
  fs.forEach(function(s){
    var i=D.series.indexOf(s);
    var bc=C[i%C.length];
    lS.push({name:s.name+' P50',type:'line',smooth:true,data:s.p50Ms,lineStyle:{width:1},itemStyle:{color:bc},emphasis:{focus:'series'}});
    lS.push({name:s.name+' P90',type:'line',smooth:true,data:s.p90Ms,lineStyle:{width:2,type:'dashed'},itemStyle:{color:bc},emphasis:{focus:'series'}});
    lS.push({name:s.name+' P99',type:'line',smooth:true,data:s.p99Ms,lineStyle:{width:2,type:'dotted'},itemStyle:{color:bc},emphasis:{focus:'series'}});
  });
  mk('c_lat',{
    tooltip:{trigger:'axis'},
    legend:{data:lS.map(function(s){return s.name;}),top:0,type:'scroll',selectedMode:'multiple'},
    grid:{top:45,bottom:35,left:55,right:20},
    xAxis:{type:'category',data:D.timeLabels,axisLabel:{rotate:30,fontSize:11}},
    yAxis:{type:'value',name:'ms'},
    series:lS
  });

  // ---- 成功 / 失败趋势 ----
  var sfS=[];
  fs.forEach(function(s){
    var i=D.series.indexOf(s);
    sfS.push({name:s.name+' 成功',type:'line',smooth:true,areaStyle:{opacity:.2},data:s.success,
      itemStyle:{color:C[i%C.length]},emphasis:{focus:'series'}});
    sfS.push({name:s.name+' 失败',type:'line',smooth:true,lineStyle:{type:'dashed'},data:s.failed,
      itemStyle:{color:C[(i+3)%C.length]},emphasis:{focus:'series'}});
  });
  mk('c_sf',{
    tooltip:{trigger:'axis'},
    legend:{data:sfS.map(function(s){return s.name;}),top:0,type:'scroll',selectedMode:'multiple'},
    grid:{top:40,bottom:35,left:55,right:20},
    xAxis:{type:'category',data:D.timeLabels,axisLabel:{rotate:30,fontSize:11}},
    yAxis:{type:'value'},
    series:sfS
  });

  // ---- 错误码饼图 ----
  var errData;
  if(!caseName){
    errData=EC;
  }else{
    errData=[];
    fs.forEach(function(s){if(s.errors){errData=errData.concat(s.errors);}});
  }
  mk('c_err',{
    tooltip:{trigger:'item',formatter:'{b}: {c} ({d}%)'},
    series:[{type:'pie',radius:['30%','55%'],data:errData,label:{formatter:'{b}\n{c} ({d}%)'}}]
  });
}

renderCharts('');

// ---- Online Users ----
var OU={{.OnlineUsersJSON}};
if(OU&&OU.series&&OU.series.length>0){
  var ouS=OU.series.map(function(s,i){
    return {name:s.name,type:'line',smooth:true,symbol:'circle',symbolSize:3,data:s.values,
      itemStyle:{color:C[i%C.length]},emphasis:{focus:'series'}};
  });
  mk('c_online_users',{
    tooltip:{trigger:'axis'},
    legend:{data:OU.series.map(function(s){return s.name;}),top:0,type:'scroll',selectedMode:'multiple'},
    grid:{top:40,bottom:35,left:55,right:20},
    xAxis:{type:'category',data:OU.timeLabels,axisLabel:{rotate:30,fontSize:11}},
    yAxis:{type:'value',name:'users'},
    series:ouS
  });
}

// ---- Metrics (dynamic) ----
var M={{.MetricsJSON}};
if(!M)M=[];
(function(){
  var metSel=document.getElementById('metricFilter');
  if(metSel&&M.length>0){
    var seen={};
    M.forEach(function(m){
      if(!seen[m.name]){seen[m.name]=true;
        var o=document.createElement('option');o.value=m.name;o.textContent=m.name;metSel.appendChild(o);
      }
    });
    metSel.addEventListener('change',function(){renderMetrics(metSel.value);});
  }
  renderMetrics('');
})();

function renderMetrics(fn){
  var mc=document.getElementById('mc');if(!mc)return;
  // dispose old
  Object.keys(charts).forEach(function(id){if(id.indexOf('c_mx')===0){charts[id].dispose();delete charts[id];}});
  mc.innerHTML='';
  var fs=fn?M.filter(function(m){return m.name===fn;}):M;
  if(!fs.length){mc.innerHTML='<div class="empty"><div class="icon">&#x1F4CA;</div><div>No metrics.</div></div>';return;}
  var prevCase='\x00';
  fs.forEach(function(m,i){
    if(m.caseGroup!==prevCase){
      prevCase=m.caseGroup;
      if(m.caseGroup){
        var hdr=document.createElement('div');hdr.className='stit';
        hdr.style.cssText='font-size:13px;margin:16px 0 10px;border-left-color:#91cc75;color:#3ba272';
        hdr.textContent='Case: '+m.caseGroup;mc.appendChild(hdr);
      }
    }
    var id='c_mx'+i;
    var bx=document.createElement('div');bx.className='bx';
    var h3=document.createElement('h3');
    h3.innerHTML=m.name+(m.labels?' <span style="font-weight:normal;color:#999">('+m.labels+')</span>':'');
    var ct=document.createElement('div');ct.className='ct';ct.id=id;
    bx.appendChild(h3);bx.appendChild(ct);mc.appendChild(bx);
    (function(cid,cm){
      mk(cid,{
        tooltip:{trigger:'axis'},
        grid:{top:20,bottom:35,left:55,right:20},
        xAxis:{type:'category',data:cm.times,axisLabel:{rotate:30,fontSize:11}},
        yAxis:{type:'value'},
        series:[{data:cm.values,type:'line',smooth:true,name:cm.name,areaStyle:{opacity:.12}}]
      });
    })(id,m);
  });
}
})();
</script>
</body>
</html>`

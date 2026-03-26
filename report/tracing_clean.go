package report

import (
	"math"
	"sort"
	"time"
)

// CleanTracingsToMetrics 将已按秒聚合的打点记录转为标准 MetricsSeries。
// QPS 系列使用 StartRecord（r.StartData=true），耗时/错误码/成功率使用 EndRecord，对每个 caseName 生成:
//
//	{name}_qps, {name}_success_qps, {name}_failed_qps,
//	{name}_avg_ms, {name}_variance_ms, {name}_min_ms, {name}_max_ms, {name}_success_rate
func CleanTracingsToMetrics(records []*TracingRecord) []*MetricsSeries {
	if len(records) == 0 {
		return nil
	}

	nameOrder := make([]string, 0)
	nameSet := make(map[string]bool)
	startGrouped := make(map[string][]*TracingRecord)
	endGrouped := make(map[string][]*TracingRecord)
	for _, r := range records {
		if r == nil || r.Count <= 0 {
			continue
		}
		if !nameSet[r.Name] {
			nameSet[r.Name] = true
			nameOrder = append(nameOrder, r.Name)
		}
		if r.StartData {
			startGrouped[r.Name] = append(startGrouped[r.Name], r)
		} else {
			endGrouped[r.Name] = append(endGrouped[r.Name], r)
		}
	}

	if len(nameOrder) == 0 {
		return nil
	}

	var result []*MetricsSeries

	for _, name := range nameOrder {
		labels := map[string]string{"case": name}
		qpsSeries := &MetricsSeries{Name: name + "_qps", Labels: labels}
		successQPSSeries := &MetricsSeries{Name: name + "_success_qps", Labels: labels}
		failedQPSSeries := &MetricsSeries{Name: name + "_failed_qps", Labels: labels}
		avgSeries := &MetricsSeries{Name: name + "_avg_ms", Labels: labels}
		varianceSeries := &MetricsSeries{Name: name + "_variance_ms", Labels: labels}
		minMsSeries := &MetricsSeries{Name: name + "_min_ms", Labels: labels}
		maxMsSeries := &MetricsSeries{Name: name + "_max_ms", Labels: labels}
		rateSeries := &MetricsSeries{Name: name + "_success_rate", Labels: labels}

		// QPS 来自 Start 记录
		startRecs := startGrouped[name]
		sort.Slice(startRecs, func(i, j int) bool { return startRecs[i].Timestamp < startRecs[j].Timestamp })
		for _, r := range startRecs {
			ts := time.Unix(r.Timestamp, 0)
			qpsSeries.Points = append(qpsSeries.Points, MetricsPoint{Timestamp: ts, Value: float64(r.Count)})
		}

		// 耗时/错误码来自 End 记录
		endRecs := endGrouped[name]
		sort.Slice(endRecs, func(i, j int) bool { return endRecs[i].Timestamp < endRecs[j].Timestamp })
		for _, r := range endRecs {
			ts := time.Unix(r.Timestamp, 0)
			total := r.Count
			successCount := r.Code[int(TracingSuccess)]
			failed := total - successCount

			avg := float64(0)
			if total > 0 {
				avg = math.Round(float64(r.TotalDurationMs)/float64(total)*10) / 10
			}
			rate := float64(0)
			if total > 0 {
				rate = math.Round(float64(successCount)/float64(total)*10000) / 100
			}

			successQPSSeries.Points = append(successQPSSeries.Points, MetricsPoint{Timestamp: ts, Value: float64(successCount)})
			failedQPSSeries.Points = append(failedQPSSeries.Points, MetricsPoint{Timestamp: ts, Value: float64(failed)})
			avgSeries.Points = append(avgSeries.Points, MetricsPoint{Timestamp: ts, Value: avg})
			varianceSeries.Points = append(varianceSeries.Points, MetricsPoint{Timestamp: ts, Value: float64(r.Variance)})
			minMsSeries.Points = append(minMsSeries.Points, MetricsPoint{Timestamp: ts, Value: float64(r.MinDurationMs)})
			maxMsSeries.Points = append(maxMsSeries.Points, MetricsPoint{Timestamp: ts, Value: float64(r.MaxDurationMs)})
			rateSeries.Points = append(rateSeries.Points, MetricsPoint{Timestamp: ts, Value: rate})
		}

		result = append(result, qpsSeries, successQPSSeries, failedQPSSeries, avgSeries, varianceSeries, minMsSeries, maxMsSeries, rateSeries)
	}

	return result
}

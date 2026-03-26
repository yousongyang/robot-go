package report

import "sort"

// CompactTracingsBySecond 将相同 (Name, Timestamp, StartData) 的 TracingRecord 合并。
// 适用于汇聚多 Agent 上报的已按秒聚合数据，使用并行 Welford（Chan's）算法合并方差。
func CompactTracingsBySecond(records []*TracingRecord) []*TracingRecord {
	if len(records) == 0 {
		return nil
	}

	type key struct {
		name  string
		ts    int64
		start bool
	}
	// acc 维护并行方差所需的中间能量
	type acc struct {
		count   int
		totalMs int64
		minMs   int64 // -1 = unset
		maxMs   int64
		mean    float64
		m2      float64 // sum of squared deviations
		codes   map[int]int
		errors  map[string]int
	}

	grouped := make(map[key]*acc, len(records))
	order := make([]key, 0, len(records))

	for _, r := range records {
		if r == nil || r.Count <= 0 {
			continue
		}
		k := key{r.Name, r.Timestamp, r.StartData}
		a := grouped[k]
		if a == nil {
			a = &acc{minMs: -1}
			grouped[k] = a
			order = append(order, k)
		}

		if !r.StartData && r.Count > 0 {
			// Chan's parallel variance: M2_AB = M2_A + M2_B + delta^2 * nA*nB/nAB
			meanB := float64(r.TotalDurationMs) / float64(r.Count)
			m2B := float64(r.Variance) * float64(r.Count)
			newCount := a.count + r.Count
			delta := meanB - a.mean
			a.mean += delta * float64(r.Count) / float64(newCount)
			a.m2 += m2B + delta*delta*float64(a.count)*float64(r.Count)/float64(newCount)
			a.totalMs += r.TotalDurationMs
			if a.minMs < 0 || r.MinDurationMs < a.minMs {
				a.minMs = r.MinDurationMs
			}
			if r.MaxDurationMs > a.maxMs {
				a.maxMs = r.MaxDurationMs
			}
		}

		a.count += r.Count

		for code, cnt := range r.Code {
			if a.codes == nil {
				a.codes = make(map[int]int, len(r.Code))
			}
			a.codes[code] += cnt
		}
		for msg, cnt := range r.Error {
			if a.errors == nil {
				a.errors = make(map[string]int, len(r.Error))
			}
			a.errors[msg] += cnt
		}
	}

	result := make([]*TracingRecord, 0, len(order))
	for _, k := range order {
		a := grouped[k]
		if a == nil || a.count <= 0 {
			continue
		}
		rec := &TracingRecord{
			Timestamp: k.ts,
			StartData: k.start,
			Name:      k.name,
			Count:     a.count,
			Code:      a.codes,
			Error:     a.errors,
		}
		if !k.start && a.count > 0 {
			rec.TotalDurationMs = a.totalMs
			if a.minMs >= 0 {
				rec.MinDurationMs = a.minMs
			}
			rec.MaxDurationMs = a.maxMs
			rec.Variance = int64(a.m2 / float64(a.count))
		}
		result = append(result, rec)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Timestamp != result[j].Timestamp {
			return result[i].Timestamp < result[j].Timestamp
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return !result[i].StartData // end (false) 在 start (true) 前
	})

	return result
}

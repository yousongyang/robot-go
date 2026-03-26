package impl

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/atframework/robot-go/report"
)

// tracingEntry 是 TracingEntry 接口的内存实现
type tracingEntry struct {
	name      string
	startTime time.Time
	tracer    *MemoryTracer
	started   bool
}

func (e *tracingEntry) Start() report.TracingEntry {
	e.startTime = time.Now()
	e.started = true
	return e
}

func (e *tracingEntry) End(code report.TracingCode, errMsg string) {
	endTime := time.Now()
	if !e.started {
		e.startTime = endTime
	}
	// 热路径：不分配 TracingRecord，直接传递原始字段给 addRecord 原地聚合
	e.tracer.addRecord(e.name, e.startTime.Unix(), endTime.Unix(),
		endTime.Sub(e.startTime).Milliseconds(), int(code), errMsg)
}

func (e *tracingEntry) EndWithError(err error) {
	if err == nil {
		e.End(report.TracingSuccess, "")
	} else {
		e.End(-1, err.Error())
	}
}

// --- 双缓冲内部结构 ---

type tracingBucketKey struct {
	name      string
	timestamp int64
	startData bool
}

// tracingBucket 是单个 (name, second, isStart) 的可变聚合状态。
// 通过 sync.Pool 复用，hot path 中不产生堆分配。
type tracingBucket struct {
	count       int
	totalMs     int64
	minMs       int64 // -1 = unset
	maxMs       int64
	meanMs      float64 // Welford 在线均值
	m2Ms        float64 // Welford M2（偏差平方和，用于方差）
	codeCounts  map[int]int
	errorCounts map[string]int
}

var tracingBucketPool = sync.Pool{
	New: func() any { return &tracingBucket{minMs: -1} },
}

func (b *tracingBucket) reset() {
	b.count = 0
	b.totalMs = 0
	b.minMs = -1
	b.maxMs = 0
	b.meanMs = 0
	b.m2Ms = 0
	for k := range b.codeCounts {
		delete(b.codeCounts, k)
	}
	for k := range b.errorCounts {
		delete(b.errorCounts, k)
	}
}

// updateEnd 更新 end 记录的统计（Welford 在线算法，无浮点数堆分配）
func (b *tracingBucket) updateEnd(durMs int64, code int, errMsg string) {
	b.count++
	b.totalMs += durMs
	if b.minMs < 0 || durMs < b.minMs {
		b.minMs = durMs
	}
	if durMs > b.maxMs {
		b.maxMs = durMs
	}
	delta := float64(durMs) - b.meanMs
	b.meanMs += delta / float64(b.count)
	b.m2Ms += delta * (float64(durMs) - b.meanMs)
	if b.codeCounts == nil {
		b.codeCounts = make(map[int]int, 4)
	}
	b.codeCounts[code]++
	if errMsg != "" && code != int(report.TracingSuccess) {
		if b.errorCounts == nil {
			b.errorCounts = make(map[string]int, 4)
		}
		b.errorCounts[errMsg]++
	}
}

type tracingBank struct {
	mu      sync.Mutex
	buckets map[tracingBucketKey]*tracingBucket
	order   []tracingBucketKey // 插入顺序，保证 snapshot 输出稳定
}

func newTracingBank() tracingBank {
	return tracingBank{
		buckets: make(map[tracingBucketKey]*tracingBucket),
	}
}

func (b *tracingBank) getOrCreate(key tracingBucketKey) *tracingBucket {
	bk := b.buckets[key]
	if bk == nil {
		bk = tracingBucketPool.Get().(*tracingBucket)
		bk.reset()
		b.buckets[key] = bk
		b.order = append(b.order, key)
	}
	return bk
}

func (b *tracingBank) snapshot() []*report.TracingRecord {
	if len(b.buckets) == 0 {
		return nil
	}
	result := make([]*report.TracingRecord, 0, len(b.buckets))
	for _, key := range b.order {
		bk := b.buckets[key]
		if bk == nil || bk.count == 0 {
			continue
		}
		rec := &report.TracingRecord{
			Timestamp: key.timestamp,
			StartData: key.startData,
			Name:      key.name,
			Count:     bk.count,
		}
		if !key.startData {
			rec.TotalDurationMs = bk.totalMs
			if bk.minMs >= 0 {
				rec.MinDurationMs = bk.minMs
			}
			rec.MaxDurationMs = bk.maxMs
			if bk.count > 1 {
				rec.Variance = int64(bk.m2Ms / float64(bk.count))
			}
			if len(bk.codeCounts) > 0 {
				rec.Code = make(map[int]int, len(bk.codeCounts))
				for k, v := range bk.codeCounts {
					rec.Code[k] = v
				}
			}
			if len(bk.errorCounts) > 0 {
				rec.Error = make(map[string]int, len(bk.errorCounts))
				for k, v := range bk.errorCounts {
					rec.Error[k] = v
				}
			}
		}
		result = append(result, rec)
	}
	return result
}

func (b *tracingBank) clear() {
	for _, bk := range b.buckets {
		if bk != nil {
			tracingBucketPool.Put(bk)
		}
	}
	// 保留底层数组，只清空 map 和 order
	for k := range b.buckets {
		delete(b.buckets, k)
	}
	b.order = b.order[:0]
}

// MemoryTracer 双缓冲区实现。
// addRecord 热路径：原子读取活跃 bank 索引 → 锁该 bank → 原地聚合（无 *TracingRecord 分配）。
// Flush：原子切换活跃 bank → 锁旧 bank 快照后清空，写入者互不阻塞。
type MemoryTracer struct {
	active int32 // atomic: 0 or 1，指向当前写入 bank
	banks  [2]tracingBank
}

func NewMemoryTracer() *MemoryTracer {
	t := &MemoryTracer{}
	t.banks[0] = newTracingBank()
	t.banks[1] = newTracingBank()
	return t
}

func (t *MemoryTracer) NewEntry(name string) report.TracingEntry {
	return &tracingEntry{name: name, tracer: t}
}

// addRecord 热路径：atomic 读 + 单 bank mutex，临界区仅做 map 查找 + 原地数值更新。
func (t *MemoryTracer) addRecord(name string, startSec, endSec, durMs int64, code int, errMsg string) {
	bankIdx := atomic.LoadInt32(&t.active)
	bank := &t.banks[bankIdx]
	bank.mu.Lock()

	// End 记录：携带全部耗时/错误码统计
	endBk := bank.getOrCreate(tracingBucketKey{name: name, timestamp: endSec, startData: false})
	endBk.updateEnd(durMs, code, errMsg)

	// Start 记录：仅计数到达数（用于观测在途请求数）
	startBk := bank.getOrCreate(tracingBucketKey{name: name, timestamp: startSec, startData: true})
	startBk.count++

	bank.mu.Unlock()
}

// Flush 原子切换活跃 bank，收割旧 bank 数据。
// 调用期间新写入直接进入备用 bank，旧 bank 的残留写入通过 mu.Lock 等待完成后一并读出。
func (t *MemoryTracer) Flush() []*report.TracingRecord {
	oldIdx := atomic.LoadInt32(&t.active)
	newIdx := 1 - oldIdx

	// 先确保备用 bank 是空的（防止上次 Flush 后有残留）
	newBank := &t.banks[newIdx]
	newBank.mu.Lock()
	newBank.clear()
	newBank.mu.Unlock()

	// 切换：之后所有新到 addRecord 进入 newIdx
	atomic.StoreInt32(&t.active, int32(newIdx))

	// 读取旧 bank（等待任何恰在切换前 load 了 oldIdx 的 addRecord 完成）
	oldBank := &t.banks[oldIdx]
	oldBank.mu.Lock()
	result := oldBank.snapshot()
	oldBank.clear()
	oldBank.mu.Unlock()

	return result
}

func (t *MemoryTracer) Reset() {
	for i := range t.banks {
		t.banks[i].mu.Lock()
		t.banks[i].clear()
		t.banks[i].mu.Unlock()
	}
}

// 编译期验证接口实现
var _ report.Tracer = (*MemoryTracer)(nil)
var _ report.TracingEntry = (*tracingEntry)(nil)

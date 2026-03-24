package atsf4g_go_robot_case

import (
	"math"
	"sync"
	"time"
)

// QPSController 令牌桶实现，保证请求均匀分布在时间轴上。
// 当 TargetQPS <= 0 时退化为不限速。
// 支持运行时动态调整速率（供 PressureController 调用）。
//
// 使用连续时间浮点累积令牌，动态计算等待间隔（而非固定 tick），
// 以精确逼近目标 QPS。桶容量上限极小（maxBurst = 1.5），
// 即使长时间卡顿后也不会累积大量令牌导致 QPS 突发。
type QPSController struct {
	mu        sync.Mutex
	targetQPS float64
	tokens    float64
	maxTokens float64
	lastTime  time.Time
}

// maxBurst 令牌桶容量上限。
// 即使长时间卡顿后也最多累积 1.5 个令牌（多发出 1 个请求），保持 QPS 平稳。
const maxBurst = 1.5

func NewQPSController(targetQPS float64) *QPSController {
	now := time.Now()
	return &QPSController{
		targetQPS: targetQPS,
		tokens:    1, // 初始 1 个令牌，允许首次请求立即发出
		maxTokens: maxBurst,
		lastTime:  now,
	}
}

// Acquire 阻塞直到获得一个令牌。
// 等待间隔根据目标 QPS 动态计算，无固定 tick 粒度限制。
func (q *QPSController) Acquire() {
	if q.targetQPS <= 0 {
		return // 不限速
	}
	for {
		q.mu.Lock()
		q.refill()
		if q.tokens >= 1 {
			q.tokens--
			q.mu.Unlock()
			return
		}
		// 精确计算等待时间：还缺多少令牌 / 每秒补充速率
		deficit := 1 - q.tokens
		waitNs := deficit / q.targetQPS * float64(time.Second)
		q.mu.Unlock()
		sleepDur := time.Duration(waitNs)
		if sleepDur < time.Millisecond {
			sleepDur = time.Millisecond // 最小休眠 1ms，避免忙等
		}
		time.Sleep(sleepDur)
	}
}

// SetQPS 动态更新目标 QPS
func (q *QPSController) SetQPS(qps float64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.refill() // 先按旧速率结算当前令牌
	if q.targetQPS > 0 && qps > 0 {
		// 按比例缩放当前令牌数，防止突发
		q.tokens = q.tokens * qps / q.targetQPS
	}
	q.targetQPS = qps
	q.maxTokens = maxBurst
	if q.tokens > q.maxTokens {
		q.tokens = q.maxTokens
	}
}

// CurrentQPS 返回当前令牌桶速率
func (q *QPSController) CurrentQPS() float64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.targetQPS
}

// refill 按连续时间补充令牌（须持有锁）。
// 使用浮点秒数 × 目标 QPS 计算应补令牌数，无整数 tick 截断。
// maxTokens 限制为 maxBurst（1.5），防止卡顿后累积大量令牌导致突发。
func (q *QPSController) refill() {
	now := time.Now()
	elapsed := now.Sub(q.lastTime)
	if elapsed <= 0 {
		return
	}
	q.lastTime = now
	tokensToAdd := elapsed.Seconds() * q.targetQPS
	q.tokens = math.Min(q.tokens+tokensToAdd, q.maxTokens)
}

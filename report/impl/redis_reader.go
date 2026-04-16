package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	redis_interface "github.com/atframework/robot-go/redis"
	"github.com/atframework/robot-go/report"
	"github.com/redis/go-redis/v9"
)

// RedisReportReader 从 Redis 读取报表数据（Master 端使用）。
type RedisReportReader struct {
	client redis_interface.RedisClient
}

// NewRedisReportReader 创建 Redis 读取器。
func NewRedisReportReader(client redis_interface.RedisClient) *RedisReportReader {
	return &RedisReportReader{client: client}
}

func (r *RedisReportReader) ReadReport(reportID string) (*report.ReportData, error) {
	if r.client == nil {
		return nil, fmt.Errorf("redis client is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rd := &report.ReportData{}

	// Meta
	metaKey := fmt.Sprintf("report:meta:%s", reportID)
	metaStr, err := r.client.Get(ctx, metaKey).Result()
	if err != nil {
		return nil, fmt.Errorf("read meta: %w", err)
	}
	if err := json.Unmarshal([]byte(metaStr), &rd.Meta); err != nil {
		return nil, fmt.Errorf("unmarshal meta: %w", err)
	}

	// Tracings (scan all agent keys)
	tracingKeys, err := scanKeys(ctx, r.client, fmt.Sprintf("report:tracing:%s:*", reportID))
	if err != nil {
		return nil, fmt.Errorf("scan tracing keys: %w", err)
	}
	for _, key := range tracingKeys {
		chunks, err := r.client.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			continue
		}
		for _, chunk := range chunks {
			var records []*report.TracingRecord
			if err := json.Unmarshal([]byte(chunk), &records); err != nil {
				continue
			}
			rd.Tracings = append(rd.Tracings, records...)
		}
	}
	rd.Tracings = report.CompactTracingsBySecond(rd.Tracings)

	// Metrics
	metricsKeys, err := scanKeys(ctx, r.client, fmt.Sprintf("report:metrics:%s:*", reportID))
	if err != nil {
		return nil, fmt.Errorf("scan metrics keys: %w", err)
	}
	for _, key := range metricsKeys {
		chunks, err := r.client.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			continue
		}
		for _, chunk := range chunks {
			var series []*report.MetricsSeries
			if err := json.Unmarshal([]byte(chunk), &series); err != nil {
				continue
			}
			rd.Metrics = append(rd.Metrics, series...)
		}
	}

	return rd, nil
}

func (r *RedisReportReader) ListReports() ([]*report.ReportMeta, error) {
	if r.client == nil {
		return nil, fmt.Errorf("redis client is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	members, err := r.client.ZRangeByScore(ctx, "report:index", &redis.ZRangeBy{
		Min: "-inf", Max: "+inf",
	}).Result()
	if err != nil {
		return nil, err
	}

	var metas []*report.ReportMeta
	for _, reportID := range members {
		key := fmt.Sprintf("report:meta:%s", reportID)
		metaStr, err := r.client.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		var meta report.ReportMeta
		if err := json.Unmarshal([]byte(metaStr), &meta); err != nil {
			continue
		}
		metas = append(metas, &meta)
	}
	return metas, nil
}

// BarrierCount 返回指定步骤中已经 ACK 的 agent 数量。
func (r *RedisReportReader) BarrierCount(reportID string, caseIndex int) (int64, error) {
	if r.client == nil {
		return 0, fmt.Errorf("redis client is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := fmt.Sprintf("task:barrier:%s:%d", reportID, caseIndex)
	return r.client.SCard(ctx, key).Result()
}

func scanKeys(ctx context.Context, client redis_interface.RedisClient, pattern string) ([]string, error) {
	if client == nil {
		return nil, fmt.Errorf("redis client is nil")
	}

	var allKeys []string
	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		allKeys = append(allKeys, keys...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return allKeys, nil
}

var _ report.ReportReader = (*RedisReportReader)(nil)

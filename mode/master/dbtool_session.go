package master

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/atframework/robot-go/mode/dbtool"
	redis_interface "github.com/atframework/robot-go/redis"
)

const (
	redisKeyDBToolPresets = "dbtool:presets"
)

// DBToolConfig DBTool 连接配置（Master 启动时传入）
type DBToolConfig struct {
	PBFile        string   `json:"pb_file"`
	RedisAddrs    []string `json:"redis_addrs"`
	RedisPassword string   `json:"redis_password,omitempty"`
	ClusterMode   bool     `json:"cluster_mode"`
	RecordPrefix  string   `json:"record_prefix"`
}

// DBToolSession 一个活跃的 dbtool 会话
type DBToolSession struct {
	config   dbtool.DBToolConfig
	client   redis_interface.RedisClient
	registry *dbtool.Registry
	querier  *dbtool.Querier
}

// DBToolManager 管理单个 dbtool 会话（Master 启动时配置）
type DBToolManager struct {
	config        dbtool.DBToolConfig
	masterRedis   redis_interface.RedisClient // master 的 Redis，用于持久化预设
	mu            sync.RWMutex
	session       *DBToolSession
	lastReloadAt  time.Time
	lastReloadErr string
}

// NewDBToolManager 创建 dbtool 管理器
func NewDBToolManager(cfg dbtool.DBToolConfig, masterRedis redis_interface.RedisClient) *DBToolManager {
	return &DBToolManager{
		config:      cfg,
		masterRedis: masterRedis,
	}
}

// Connect 连接 Redis + 加载 PB，创建会话
func (dm *DBToolManager) Connect() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if dm.session != nil {
		return nil // already connected
	}
	return dm.connectLocked()
}

// connectLocked 内部连接逻辑，调用前必须持有 dm.mu 写锁
func (dm *DBToolManager) connectLocked() error {
	dm.lastReloadAt = time.Now()

	if dbtool.GetTableExtractor() == nil {
		dm.lastReloadErr = "no TableExtractor registered"
		return fmt.Errorf("%s", dm.lastReloadErr)
	}

	// 加载 PB
	registry := dbtool.NewRegistry(dbtool.GetTableExtractor())
	if err := registry.LoadPBFile(dm.config.PBFile); err != nil {
		dm.lastReloadErr = fmt.Sprintf("load pb file: %v", err)
		return fmt.Errorf("load pb file: %w", err)
	}
	if len(registry.GetAllTables()) == 0 {
		dm.lastReloadErr = "no tables found in pb file"
		return fmt.Errorf("%s", dm.lastReloadErr)
	}

	// 连接 Redis
	client, err := redis_interface.NewClient(dm.config.RedisConfig)
	if err != nil {
		dm.lastReloadErr = fmt.Sprintf("connect redis: %v", err)
		return fmt.Errorf("connect redis: %w", err)
	}

	querier := dbtool.NewQuerier(client, registry, dm.config.RecordPrefix)
	dm.session = &DBToolSession{
		config:   dm.config,
		client:   client,
		registry: registry,
		querier:  querier,
	}
	dm.lastReloadErr = ""
	log.Printf("[DBTool] Connected (tables: %d)", len(registry.GetAllTables()))
	return nil
}

// Reload 强制重新加载 .pb 文件并重连 Redis
func (dm *DBToolManager) Reload() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	// 关闭旧会话
	if dm.session != nil && dm.session.client != nil {
		dm.session.client.Close()
	}
	dm.session = nil

	log.Printf("[DBTool] Reloading...")
	return dm.connectLocked()
}

// StartAutoReload 启动定期自动 reload goroutine
func (dm *DBToolManager) StartAutoReload(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	log.Printf("[DBTool] Auto-reload enabled, interval=%v", interval)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := dm.Reload(); err != nil {
					log.Printf("[DBTool] Auto-reload failed: %v", err)
				} else {
					log.Printf("[DBTool] Auto-reload succeeded")
				}
			}
		}
	}()
}

// GetLastReloadInfo 返回上次 reload 时间和错误信息
func (dm *DBToolManager) GetLastReloadInfo() (time.Time, string) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.lastReloadAt, dm.lastReloadErr
}

// GetSession 获取活跃会话（未连接时返回 nil）
func (dm *DBToolManager) GetSession() *DBToolSession {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.session
}

// Close 关闭会话
func (dm *DBToolManager) Close() {
	dm.mu.Lock()
	s := dm.session
	dm.session = nil
	dm.mu.Unlock()
	if s != nil && s.client != nil {
		s.client.Close()
		log.Printf("[DBTool] Session closed")
	}
}

// --- 预查询方案 (Presets) ---

// DBToolPresetKeyConfig 预设中单个 Key 字段的配置
type DBToolPresetKeyConfig struct {
	Field      string `json:"field"`                 // 原始字段名
	Alias      string `json:"alias,omitempty"`       // 别名（展示用）
	FixedValue string `json:"fixed_value,omitempty"` // 定死的值（非空时前端不显示输入框）
}

// DBToolPreset 预查询方案
type DBToolPreset struct {
	Name      string                  `json:"name"`                 // 方案名
	Table     string                  `json:"table"`                // message 名
	Index     string                  `json:"index"`                // index 名
	Keys      []DBToolPresetKeyConfig `json:"keys"`                 // key 字段配置
	ExtraArgs []string                `json:"extra_args,omitempty"` // 预设的额外参数（如 sorted set sub cmd）
	CreatedAt string                  `json:"created_at,omitempty"`
}

// SavePreset 保存预查询方案到 Redis
func (dm *DBToolManager) SavePreset(preset DBToolPreset) error {
	if preset.Name == "" {
		return fmt.Errorf("preset name is required")
	}
	if preset.CreatedAt == "" {
		preset.CreatedAt = time.Now().Format(time.RFC3339)
	}
	data, err := json.Marshal(preset)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return dm.masterRedis.HSet(ctx, redisKeyDBToolPresets, preset.Name, string(data)).Err()
}

// DeletePreset 删除预查询方案
func (dm *DBToolManager) DeletePreset(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return dm.masterRedis.HDel(ctx, redisKeyDBToolPresets, name).Err()
}

// ListPresets 列出所有预查询方案
func (dm *DBToolManager) ListPresets() ([]DBToolPreset, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := dm.masterRedis.HGetAll(ctx, redisKeyDBToolPresets).Result()
	if err != nil {
		return nil, err
	}
	presets := make([]DBToolPreset, 0, len(result))
	for _, v := range result {
		var p DBToolPreset
		if err := json.Unmarshal([]byte(v), &p); err != nil {
			continue
		}
		presets = append(presets, p)
	}
	return presets, nil
}

// --- 查询 API ---

// DBToolTableSummary 表概览信息（给前端用）
type DBToolTableSummary struct {
	MessageName string            `json:"message_name"`
	FullName    string            `json:"full_name"`
	Indexes     []DBToolIndexInfo `json:"indexes"`
}

// DBToolIndexInfo 索引信息
type DBToolIndexInfo struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	KeyFields     []string `json:"key_fields"`
	EnableCAS     bool     `json:"enable_cas"`
	MaxListLength uint32   `json:"max_list_length,omitempty"`
}

// ListTables 列出当前会话的所有表
func (s *DBToolSession) ListTables() []DBToolTableSummary {
	tables := s.registry.GetAllTables()
	result := make([]DBToolTableSummary, 0, len(tables))
	for _, t := range tables {
		summary := DBToolTableSummary{
			MessageName: string(t.MessageDesc.Name()),
			FullName:    string(t.MessageFullName),
		}
		for _, idx := range t.Indexes {
			summary.Indexes = append(summary.Indexes, DBToolIndexInfo{
				Name:          idx.Name,
				Type:          idx.Type.String(),
				KeyFields:     idx.KeyFields,
				EnableCAS:     idx.EnableCAS,
				MaxListLength: idx.MaxListLength,
			})
		}
		result = append(result, summary)
	}
	return result
}

// ExecuteQuery 执行查询
func (s *DBToolSession) ExecuteQuery(tableName, indexName string, keyValues []string, extraArgs []string) (string, error) {
	info := s.registry.FindTableByShortName(tableName)
	if info == nil {
		return "", fmt.Errorf("unknown table: %s", tableName)
	}

	var index *dbtool.TableIndex
	for i := range info.Indexes {
		if info.Indexes[i].Name == indexName {
			index = &info.Indexes[i]
			break
		}
	}
	if index == nil {
		return "", fmt.Errorf("unknown index: %s for table %s", indexName, tableName)
	}

	if len(keyValues) < len(index.KeyFields) {
		return "", fmt.Errorf("index '%s' requires %d key field(s): [%s], provided: %d",
			index.Name, len(index.KeyFields), joinStr(index.KeyFields, ", "), len(keyValues))
	}

	switch index.Type {
	case dbtool.IndexTypeKV:
		return s.querier.QueryKV(info, index, keyValues)
	case dbtool.IndexTypeKL:
		listIndex := int64(-1)
		if len(extraArgs) > 0 {
			fmt.Sscanf(extraArgs[0], "%d", &listIndex)
		}
		return s.querier.QueryKL(info, index, keyValues, listIndex)
	case dbtool.IndexTypeSortedSet:
		return s.executeSortedSetQuery(index, keyValues, extraArgs)
	default:
		return "", fmt.Errorf("unsupported index type: %s", index.Type)
	}
}

func (s *DBToolSession) executeSortedSetQuery(index *dbtool.TableIndex, keyValues []string, extraArgs []string) (string, error) {
	if len(extraArgs) == 0 {
		return s.querier.QuerySortedSetCount(index, keyValues)
	}

	subCmd := extraArgs[0]
	switch subCmd {
	case "count":
		return s.querier.QuerySortedSetCount(index, keyValues)
	case "rank", "rrank":
		if len(extraArgs) < 3 {
			return "", fmt.Errorf("usage: %s <start> <stop>", subCmd)
		}
		var start, stop int64
		fmt.Sscanf(extraArgs[1], "%d", &start)
		fmt.Sscanf(extraArgs[2], "%d", &stop)
		return s.querier.QuerySortedSetByRank(index, keyValues, start, stop, subCmd == "rrank")
	case "score", "rscore":
		if len(extraArgs) < 3 {
			return "", fmt.Errorf("usage: %s <min> <max> [offset] [count]", subCmd)
		}
		min := extraArgs[1]
		max := extraArgs[2]
		var offset, count int64
		count = 20
		if len(extraArgs) > 3 {
			fmt.Sscanf(extraArgs[3], "%d", &offset)
		}
		if len(extraArgs) > 4 {
			fmt.Sscanf(extraArgs[4], "%d", &count)
		}
		return s.querier.QuerySortedSetByScore(index, keyValues, min, max, offset, count, subCmd == "rscore")
	default:
		return "", fmt.Errorf("unknown sorted set subcommand: %s (available: count, rank, rrank, score, rscore)", subCmd)
	}
}

func joinStr(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

// Package robot_redis 提供 Redis 连接抽象，支持集群与非集群模式。
package robot_redis

import (
	"context"
	"flag"
	"fmt"
	"time"

	utils "github.com/atframework/robot-go/utils"
	"github.com/redis/go-redis/v9"
)

// Config Redis 连接配置
type Config struct {
	Addrs       []string `yaml:"addrs"`        // Redis 地址列表（非集群只取第一个）
	Password    string   `yaml:"password"`     // 密码
	ClusterMode bool     `yaml:"cluster_mode"` // 是否集群模式
}

// 统一的 Redis 客户端抽象
type RedisClient interface {
	redis.SetCmdable
	redis.HashCmdable
	redis.SortedSetCmdable
	redis.GenericCmdable
	redis.StringCmdable
	redis.ListCmdable
	Close() error
}

func RegisterFlags(flagSet *flag.FlagSet) *flag.FlagSet {
	flagSet.Var(&utils.StringSliceFlag{}, "redis-addr", "Redis address (e.g. localhost:6379). If empty, Redis is disabled.")
	flagSet.String("redis-pwd", "", "Redis password.")
	flagSet.String("cluster-mode", "false", "Redis cluster mode.")
	return flagSet
}

func ParseConfig(flagSet *flag.FlagSet) Config {
	addrs := utils.ParseSliceFlags(flagSet, "redis-addr")
	password := flagSet.Lookup("redis-pwd").Value.String()
	clusterMode := flagSet.Lookup("cluster-mode").Value.String() == "true"
	return Config{
		Addrs:       addrs,
		Password:    password,
		ClusterMode: clusterMode,
	}
}

// NewClient 根据配置创建 Redis 客户端并进行 Ping 检测。
func NewClient(cfg Config) (RedisClient, error) {
	if len(cfg.Addrs) == 0 {
		return nil, fmt.Errorf("redis addrs is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if cfg.ClusterMode {
		opts := &redis.ClusterOptions{
			Addrs:    cfg.Addrs,
			Password: cfg.Password,
		}
		client := redis.NewClusterClient(opts)
		if err := client.Ping(ctx).Err(); err != nil {
			client.Close()
			return nil, fmt.Errorf("redis cluster ping %v: %w", cfg.Addrs, err)
		}
		return client, nil
	}

	opts := &redis.Options{
		Addr:     cfg.Addrs[0],
		Password: cfg.Password,
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis ping %s: %w", cfg.Addrs[0], err)
	}
	return client, nil
}

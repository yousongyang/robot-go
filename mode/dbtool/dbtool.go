package dbtool

import (
	"flag"
	"fmt"
	"os"
	"strings"

	host "github.com/atframework/atframe-utils-go/host"
	redis_interface "github.com/atframework/robot-go/redis"
	utils "github.com/atframework/robot-go/utils"
)

// RegisterFlags 注册 dbtool 模式需要的 flag
func RegisterFlags(flagSet *flag.FlagSet) {
	flagSet.String("dbtool-redis-addr", "localhost:6379", "Redis address (comma separated for cluster)")
	flagSet.String("dbtool-redis-password", "", "Redis password")
	flagSet.Bool("dbtool-redis-cluster", false, "Whether Redis is in cluster mode")
	flagSet.String("dbtool-pb-file", "", "path to .pb (FileDescriptorSet) file containing proto definitions")
	flagSet.String("dbtool-record-prefix", "", "Redis key record prefix (overrides random-prefix)")
	flagSet.String("dbtool-random-prefix", "", "use GetStableHostID as record prefix (true/false)")
	flagSet.Int64("dbtool-redis-version", 0, "random-prefix version")
}

var tableExtractor TableExtractor = nil

func RegisterDatabaseTableExtractor(extractor TableExtractor) {
	tableExtractor = extractor
}

func GetTableExtractor() TableExtractor {
	return tableExtractor
}

func ParseDBTOOLRedisConfig(flagSet *flag.FlagSet) redis_interface.Config {
	addrs := utils.GetFlagString(flagSet, "dbtool-redis-addr")
	if addrs == "" {
		addrs = ""
	}
	// 去除addrs 前后的[]
	addrs = strings.Trim(addrs, "[")
	addrs = strings.Trim(addrs, "]")
	addrsList := strings.Split(addrs, " ")
	password := flagSet.Lookup("dbtool-redis-password").Value.String()
	clusterMode := flagSet.Lookup("dbtool-redis-cluster").Value.String() == "true"
	return redis_interface.Config{
		Addrs:       addrsList,
		Password:    password,
		ClusterMode: clusterMode,
	}
}

type DBToolConfig struct {
	PBFile       string                 `json:"pb_file"`
	RecordPrefix string                 `json:"record_prefix"`
	RedisConfig  redis_interface.Config `json:"redis_config"`
}

func LoadDBToolConfig(flagSet *flag.FlagSet) (DBToolConfig, error) {
	pbFile := utils.GetFlagString(flagSet, "dbtool-pb-file")
	if pbFile == "" {
		return DBToolConfig{}, fmt.Errorf("missing required flag: --dbtool-pb-file")
	}

	// 构建 Redis 配置
	redisCfg := ParseDBTOOLRedisConfig(flagSet)

	// 确定 record prefix
	recordPrefix := utils.GetFlagString(flagSet, "dbtool-record-prefix")
	if recordPrefix == "" {
		randomPrefix := utils.GetFlagString(flagSet, "dbtool-random-prefix")
		if randomPrefix == "true" {
			version := utils.GetFlagInt32(flagSet, "dbtool-redis-version")
			recordPrefix = host.GetStableHostID(version)
			fmt.Printf("Using stable host ID as record prefix: %s\n", recordPrefix)
		} else {
			recordPrefix = "default"
		}
	}
	fmt.Printf("Record prefix: %s\n", recordPrefix)

	return DBToolConfig{
		PBFile:       pbFile,
		RecordPrefix: recordPrefix,
		RedisConfig:  redisCfg,
	}, nil
}

// 启动 dbtool
func Start(flagSet *flag.FlagSet) {
	config, err := LoadDBToolConfig(flagSet)
	if err != nil {
		fmt.Printf("Load DBTool config error: %v\n", err)
		os.Exit(1)
	}

	// 加载 .pb 文件
	fmt.Printf("Loading proto descriptors from: %s\n", config.PBFile)
	if GetTableExtractor() == nil {
		fmt.Println("No TableExtractor registered, need RegisterDatabaseTableExtractor() to extract table info from proto descriptors")
		os.Exit(1)
	}
	registry := NewRegistry(GetTableExtractor())
	if err := registry.LoadPBFile(config.PBFile); err != nil {
		fmt.Printf("Load pb file error: %v\n", err)
		os.Exit(1)
	}

	tables := registry.GetAllTables()
	if len(tables) == 0 {
		fmt.Println("No tables with database_table option found in the pb file")
		os.Exit(1)
	}
	fmt.Printf("Found %d table(s)\n", len(tables))

	// 连接 Redis
	fmt.Printf("Connecting to Redis: %v (cluster: %v)\n", config.RedisConfig.Addrs, config.RedisConfig.ClusterMode)
	client, err := redis_interface.NewClient(config.RedisConfig)
	if err != nil {
		fmt.Printf("Connect Redis error: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	fmt.Println("Redis connected")

	// 启动交互式 Shell
	querier := NewQuerier(client, registry, config.RecordPrefix)
	shell := NewShell(querier, registry)
	if err := shell.Run(); err != nil {
		fmt.Printf("Shell error: %v\n", err)
		os.Exit(1)
	}
}

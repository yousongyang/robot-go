// Package main 提供 Master 的独立二进制入口。
// 编译后仅需 Redis 即可启动分布式压测调度端，无需业务 protobuf 依赖。
//
//	go build -o robot-master ./bin
//	./robot-master -listen :8080 -redis-addr localhost:6379
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atframework/robot-go/master"
	"gopkg.in/yaml.v3"
)

func main() {
	fs := flag.NewFlagSet(filepath.Base(os.Args[0]), flag.ContinueOnError)
	fs.Bool("h", false, "show help")
	fs.Bool("help", false, "show help")

	fs.String("config", "", "yaml config file path")
	fs.String("listen", ":8080", "HTTP listen address")
	fs.String("redis-addr", "localhost:6379", "Redis address")
	fs.String("redis-pwd", "", "Redis password")
	fs.Int("redis-db", 0, "Redis DB index")
	fs.String("report-dir", "../report", "HTML report output directory")
	fs.String("report-expiry", "", "auto-delete reports after duration (e.g. 168h=7d; empty=never)")

	// 先从命令行尝试提取 -config 路径
	args := os.Args[1:]
	yamlPath := extractConfigPath(args)
	if yamlPath != "" {
		if err := applyYAML(fs, yamlPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if fs.Lookup("help").Value.String() == "true" || fs.Lookup("h").Value.String() == "true" {
		fs.PrintDefaults()
		return
	}

	cfg := master.MasterConfig{
		ListenAddr: fs.Lookup("listen").Value.String(),
		RedisAddr:  fs.Lookup("redis-addr").Value.String(),
		RedisPwd:   fs.Lookup("redis-pwd").Value.String(),
		ReportDir:  fs.Lookup("report-dir").Value.String(),
	}
	if v := fs.Lookup("redis-db").Value.String(); v != "" {
		fmt.Sscanf(v, "%d", &cfg.RedisDB)
	}
	if v := fs.Lookup("report-expiry").Value.String(); v != "" && v != "0" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ReportExpiry = d
		} else {
			fmt.Fprintf(os.Stderr, "invalid report-expiry %q: %v\n", v, err)
		}
	}

	m, err := master.NewMaster(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init master: %v\n", err)
		os.Exit(1)
	}
	if err := m.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "master: %v\n", err)
		os.Exit(1)
	}
}

func extractConfigPath(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-config" || arg == "--config" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if after, ok := strings.CutPrefix(arg, "-config="); ok {
			return after
		}
		if after, ok := strings.CutPrefix(arg, "--config="); ok {
			return after
		}
	}
	return ""
}

func applyYAML(fs *flag.FlagSet, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	for key, val := range cfg {
		if fs.Lookup(key) != nil {
			fs.Set(key, fmt.Sprintf("%v", val))
		}
	}
	return nil
}

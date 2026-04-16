package atsf4g_go_robot_user

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	log "github.com/atframework/atframe-utils-go/log"
	agent "github.com/atframework/robot-go/agent"
	base "github.com/atframework/robot-go/base"
	robot_case "github.com/atframework/robot-go/case"
	_ "github.com/atframework/robot-go/cmd"
	conn "github.com/atframework/robot-go/conn"
	gatewayconn "github.com/atframework/robot-go/conn/atgateway"
	user_interface "github.com/atframework/robot-go/data"
	user_impl "github.com/atframework/robot-go/data/impl"
	redis_interface "github.com/atframework/robot-go/redis"
	solo "github.com/atframework/robot-go/solo"
	utils "github.com/atframework/robot-go/utils"
	"gopkg.in/yaml.v3"
)

func NewRobotFlagSet() *flag.FlagSet {
	flagSet := flag.NewFlagSet(
		fmt.Sprintf("%s [options...]", filepath.Base(os.Args[0])), flag.ContinueOnError)
	flagSet.Bool("h", false, "show help")
	flagSet.Bool("help", false, "show help")

	flagSet.String("config", "", "yaml config file path")
	flagSet.String("mode", "", "run mode: (empty)=standalone, agent, solo")
	flagSet.String("case_file", "", "case file path")
	flagSet.Int("case_file_repeated", 1, "case file repeated time")
	flagSet.Var(&utils.StringSliceFlag{}, "set", "set variable for case file: --set KEY=VALUE (repeatable)")

	// 链接层配置
	conn.RegisterFlags(flagSet)
	// Redis 相关配置
	redis_interface.RegisterFlags(flagSet)
	// Agent模式
	agent.RegisterFlags(flagSet)
	// 单节点压测模式
	solo.RegisterFlags(flagSet)

	return flagSet
}

// LoadFlagSetFromYAML reads a flat YAML config file and writes its values into an unparsed FlagSet,
// then parses the FlagSet with the given args. Command line args have higher priority than YAML values.
// If yamlPath is empty, it tries to extract the path from -config/--config in args.
func LoadFlagSetFromYAML(flagSet *flag.FlagSet, yamlPath string, args []string) error {
	if yamlPath == "" {
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "-config" || arg == "--config" {
				if i+1 < len(args) {
					yamlPath = args[i+1]
				}
				break
			}
			if after, found := strings.CutPrefix(arg, "-config="); found {
				yamlPath = after
				break
			}
			if after, found := strings.CutPrefix(arg, "--config="); found {
				yamlPath = after
				break
			}
		}
		if yamlPath == "" {
			_, err := os.Stat("config.yaml")
			if err == nil {
				yamlPath = "config.yaml"
			}
		}
	}

	if yamlPath != "" {
		fmt.Println("Load Config From: ", yamlPath)

		data, err := os.ReadFile(yamlPath)
		if err != nil {
			return fmt.Errorf("read yaml config file %s: %w", yamlPath, err)
		}
		var config map[string]interface{}
		if err := yaml.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse yaml config file %s: %w", yamlPath, err)
		}

		for key, value := range config {
			if key == "set" {
				// 特殊处理：set 支持 map/list/string 三种 YAML 写法
				continue
			}
			if flagSet.Lookup(key) != nil {
				flagSet.Set(key, fmt.Sprintf("%v", value))
			}
		}
		// 处理 set 变量：支持 map、list、string 三种写法
		if rawSet, ok := config["set"]; ok && flagSet.Lookup("set") != nil {
			switch v := rawSet.(type) {
			case map[string]interface{}:
				for k, val := range v {
					flagSet.Set("set", fmt.Sprintf("%s=%v", k, val))
				}
			case []interface{}:
				for _, item := range v {
					flagSet.Set("set", fmt.Sprintf("%v", item))
				}
			case string:
				if v != "" {
					flagSet.Set("set", v)
				}
			}
		}
	}

	return flagSet.Parse(args)
}

// StartRobot starts the robot. The flagSet should already be parsed (e.g., via LoadFlagSetFromYAML).
func StartRobot(flagSet *flag.FlagSet, unpack user_interface.UserReceiveUnpackFunc, createMsg user_interface.UserReceiveCreateMessageFunc) {
	if flagSet.Lookup("help").Value.String() == "true" ||
		flagSet.Lookup("h").Value.String() == "true" {
		flagSet.PrintDefaults()
		return
	}

	if unpack == nil || createMsg == nil {
		fmt.Println("unpack or createMsg function is nil")
		return
	}
	user_interface.RegisterCreateUser(user_impl.CreateUser, unpack, createMsg)

	var connectType string
	if flagSet.Lookup("connect-type").Value.String() != "" {
		connectType = flagSet.Lookup("connect-type").Value.String()
	}

	switch connectType {
	case "atgateway":
		cfg := gatewayconn.ParseGatewayConfig(flagSet)
		base.ConnectFunc = func() (conn.Connection, error) {
			return gatewayconn.DialGateway(base.Url, cfg)
		}
	}

	base.Url = flagSet.Lookup("url").Value.String()
	fmt.Println("URL:", base.Url)

	mode := flagSet.Lookup("mode").Value.String()
	switch mode {
	case "agent":
		fmt.Println("Starting in Agent mode")
		startAgent(flagSet, unpack, createMsg)
		return
	case "solo":
		fmt.Println("Starting in Solo mode (single-node stress test)")
		solo.StartSolo(flagSet)
		return
	}

	// --- Standalone 模式 ---
	caseFile := flagSet.Lookup("case_file").Value.String()
	if caseFile != "" {
		repeatedTimeString := flagSet.Lookup("case_file_repeated").Value.String()
		var repeatedTime int32 = 1
		if repeatedTimeString != "" {
			temp, err := strconv.Atoi(repeatedTimeString)
			if err != nil {
				fmt.Println("Invalid case_file_repeated value:", repeatedTimeString)
				return
			}
			repeatedTime = int32(temp)
		}
		err := robot_case.RunCaseFileStandAlone(caseFile, repeatedTime, utils.GetSetVars(flagSet))
		if err != nil {
			fmt.Println("Run case file error:", err)
			log.CloseAllLogWriters()
			os.Exit(1)
		}
	} else {
		utils.ReadLine()
	}

	utils.StdoutLog("Closing all pending connections")
	user_interface.LogoutAllUsers()
	log.CloseAllLogWriters()
	utils.StdoutLog("Exiting....")
}

// startAgent 以 Agent 模式启动
func startAgent(flagSet *flag.FlagSet, unpack user_interface.UserReceiveUnpackFunc, createMsg user_interface.UserReceiveCreateMessageFunc) {
	cfg := agent.AgentConfig{
		RedisConfig: redis_interface.ParseConfig(flagSet),
		MasterAddr:  utils.GetFlagString(flagSet, "master-addr"),
		AgentID:     utils.GetFlagString(flagSet, "agent-id"),
		GroupID:     utils.GetFlagString(flagSet, "agent-group"),
	}
	a, err := agent.NewAgent(cfg)
	if err != nil {
		fmt.Println("Agent init error:", err)
		os.Exit(1)
	}
	if err := a.Start(); err != nil {
		fmt.Println("Agent error:", err)
		os.Exit(1)
	}
}

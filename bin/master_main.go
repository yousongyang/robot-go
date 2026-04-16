// Package main 提供 Master 的独立二进制入口。
// 编译后仅需 Redis 即可启动分布式压测调度端，无需业务 protobuf 依赖。
//
//	go build -o robot-master ./bin
//	./robot-master -listen :8080 -redis-addr localhost:6379
package main

import (
	"fmt"
	"os"

	robot "github.com/atframework/robot-go"
	"github.com/atframework/robot-go/mode/master"
)

func main() {
	flagSet := robot.NewRobotFlagSet()
	if err := robot.LoadFlagSetFromYAML(flagSet, "", os.Args[1:]); err != nil {
		fmt.Println(err)
		return
	}

	master.StartMaster(flagSet)
}

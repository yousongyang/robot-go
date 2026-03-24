# robot-go

通用的 Go 语言机器人测试客户端框架，提供 WebSocket 连接管理、交互式命令行、任务调度和批量用例执行等基础设施，用于模拟用户与服务器的交互。

## 模块结构

```
robot-go/
├── robot.go            # 框架入口，提供 NewRobotFlagSet() 和 StartRobot()
├── base/
│   ├── config.go       # 全局配置（SocketUrl 等）
│   └── task_action.go  # 任务执行框架（TaskActionImpl / TaskActionBase / TaskActionManager）
├── case/
│   └── action.go       # 批量用例执行框架（RegisterCase / RunCaseFile）
├── cmd/
│   └── user.go         # 用户管理与命令路由（RegisterUserCommand / GetCurrentUser）
├── data/
│   ├── user.go         # User 接口定义及工厂注册
│   ├── action.go       # TaskActionUser（带用户上下文的任务）
│   └── impl/
│       └── user.go     # User 接口实现（WebSocket / RPC / 心跳）
└── utils/
    ├── readline.go     # 交互式命令行（RegisterCommand / 自动补全 / 命令树）
    └── history.go      # 命令历史管理
```

## 快速开始

### 安装

```bash
go get github.com/atframework/robot-go
```

### 最小使用示例

```go
package main

import (
    "os"

    "google.golang.org/protobuf/proto"

    robot "github.com/atframework/robot-go"
)

func UnpackMessage(msg proto.Message) (rpcName string, typeName string, errorCode int32,
    msgHead proto.Message, bodyBin []byte, sequence uint64, err error) {
    // 从服务端返回的消息中解析出 rpcName、errorCode、sequence 等字段
    // ...
    return
}

func main() {
    flagSet := robot.NewRobotFlagSet()
    // 可添加自定义 flag
    // flagSet.String("resource", "", "resource directory")

    if err := flagSet.Parse(os.Args[1:]); err != nil {
        return
    }

    robot.StartRobot(flagSet, UnpackMessage, func() proto.Message {
        // 返回服务端消息的 protobuf 外层包装类型
        return &YourCSMsg{}
    })
}
```

### 命令行参数

#### 通用参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-config` | `config.yaml` | YAML 配置文件路径（所有参数均可写入 YAML，命令行优先） |
| `-url` | `ws://localhost:7001/ws/v1` | 服务器 WebSocket 地址 |
| `-connect-type` | `websocket` | 连接类型：`websocket`、`atgateway` |
| `-access-token` | 空 | atgateway 模式：认证 Token |
| `-key-exchange` | `none` | atgateway 模式：ECDH 算法（`none`/`x25519`/`p256`/…） |
| `-crypto` | `none` | atgateway 模式：加密算法（`none`/`aes-128-gcm`/`chacha20`/…） |
| `-compression` | `none` | atgateway 模式：压缩算法（`none`/`zstd`/`lz4`/`snappy`/`zlib`） |
| `-case_file` | 空 | 用例配置文件路径（支持普通用例文件和压测文件） |
| `-case_file_repeated` | `1` | 用例文件执行次数 |
| `-report-dir` | `../report` | 报告输出目录（非空则启用报告系统） |
| `-h` / `-help` | | 显示帮助信息 |

#### 分布式模式参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-mode` | 空（standalone） | 运行模式：空=单机，`master`，`agent` |
| `-redis-addr` | `localhost:6379` | Redis 地址（Master/Agent 数据交换） |
| `-redis-pwd` | 空 | Redis 密码 |
| `-redis-db` | `0` | Redis 数据库编号 |
| `-listen` | `:8080` | HTTP 监听地址（Master 模式） |
| `-master-addr` | 空 | Master HTTP 地址（Agent 模式必填），如 `http://192.168.1.10:8080` |
| `-agent-id` | 空（自动生成） | Agent 唯一 ID，用于区分多个 Agent |
| `-agent-group` | 空 | Agent 组 ID，Master 可按组分发任务 |

所有参数均可通过 YAML 配置文件指定（去掉前缀 `-`），命令行参数优先级更高。

## 核心概念

### User 接口

`data.User` 定义了与服务器交互的用户抽象，主要能力：

- **连接管理**: WebSocket 连接、登录/登出、心跳
- **RPC 通信**: `SendReq()` 发送请求并等待响应，通过 sequence 匹配
- **推送消息**: `RegisterMessageHandler()` 注册服务端主动推送的消息处理器
- **任务执行**: `RunTask()` / `RunTaskDefaultTimeout()` 在用户上下文中执行异步任务
- **扩展数据**: `GetExtralData()` / `SetExtralData()` 存储自定义数据

使用框架时需要实现两个回调函数并传入 `StartRobot()`：

```go
// UserReceiveUnpackFunc - 解析服务端返回的消息
type UserReceiveUnpackFunc func(proto.Message) (
    rpcName string, typeName string, errorCode int32,
    msgHead proto.Message, bodyBin []byte, sequence uint64, err error)

// UserReceiveCreateMessageFunc - 创建服务端消息的 protobuf 实例
type UserReceiveCreateMessageFunc func() proto.Message
```

### 任务系统 (TaskAction)

任务基于 channel 的 Yield/Resume 模式实现协作式调度：

- `TaskActionBase`: 基础任务实现，支持超时控制、任务链（`AwaitTask`）
- `TaskActionUser`: 绑定 User 上下文的任务，RPC 等待期间自动释放 User 操作锁
- `TaskActionCase`: 用例任务，用于批量执行场景
- `TaskActionManager`: 管理任务生命周期（分配 ID、超时计时器、等待完成）

```go
// 在用户上下文中执行任务
user.RunTaskDefaultTimeout(func(action *user_data.TaskActionUser) error {
    errCode, rsp, err := protocol.SomeRpc(action)
    if err != nil {
        return err
    }
    // 处理响应...
    return nil
}, "Task Name")
```

### 命令注册

通过 `utils.RegisterCommand()` 或 `cmd.RegisterUserCommand()` 注册交互式命令：

```go
func init() {
    // 注册普通命令（无需当前用户上下文）
    utils.RegisterCommandDefaultTimeout(
        []string{"user", "login"},  // 命令路径
        LoginCmd,                    // 处理函数
        "<openid>",                  // 参数说明
        "登录协议",                   // 命令描述
        nil,                         // 自动补全函数
    )

    // 注册用户命令（自动注入当前用户）
    cmd.RegisterUserCommand(
        []string{"user", "getInfo"},
        GetInfoCmd,
        "",
        "拉取用户信息",
        nil,
    )
}
```

交互模式下输入 `help` 可查看所有已注册命令。命令支持 Tab 自动补全。

### 用例系统 (Case)

用例系统用于批量自动化测试，支持并发执行和进度显示。

#### 注册用例

```go
func init() {
    robot_case.RegisterCase("login", LoginCase, time.Second*5)
    robot_case.RegisterCase("logout", LogoutCase, time.Second*5)
}

func LoginCase(action *robot_case.TaskActionCase, openId string, args []string) error {
    u := user_data.CreateUser(openId, base.SocketUrl, action.Log, false)
    if u == nil {
        return fmt.Errorf("failed to create user")
    }

    err := action.AwaitTask(u.RunTaskDefaultTimeout(LoginTask, "Login Task"))
    if err != nil {
        return err
    }
    return nil
}
```

#### 用例配置文件

#### 普通用例配置文件

通过 `-case_file` 指定配置文件自动执行用例，格式为：

```
<case_name> <openid_prefix> <user_count> <batch_count> <iterations> [args...] [&]
```

| 字段 | 说明 |
|------|------|
| `case_name` | 已注册的用例名称 |
| `openid_prefix` | 用户 OpenID 前缀，自动追加序号 0~(user_count-1) |
| `user_count` | 模拟用户数量 |
| `batch_count` | 最大并发数 |
| `iterations` | 每个用户执行次数 |
| `args` | 传递给用例的额外参数 |
| `&` | 行尾加 `&` 表示异步执行，不等待完成即执行下一行 |

以 `#` 开头的行为注释。示例：

```conf
# 登录
login 1250001 60 60 1
# 并发 GetInfo 测试
run_cmd 1250001 60 60 1 user getInfo &
run_cmd 1250001 60 60 1 user getInfo &
run_cmd 1250001 60 60 1 user getInfo
# 登出
logout 1250001 60 60 1
```

#### 压测用例配置文件（`#!stress`）

文件首行为 `#!stress` 时，切换到压测模式。每行定义一个压测用例：

```
<case_name> <error_break> <openid_prefix> <id_start> <id_end> <batch_count> <target_qps> <run_time> [args...]
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `case_name` | string | 已注册的用例名称 |
| `error_break` | bool | `true`/`false`：遇到第一个错误是否立即停止 |
| `openid_prefix` | string | 账号 ID 前缀 |
| `id_start` | int64 | 账号起始编号（含） |
| `id_end` | int64 | 账号结束编号（不含） |
| `batch_count` | int64 | 最大并发数 |
| `target_qps` | float64 | 目标 QPS，`0` 表示不限速 |
| `run_time` | int64 | 持续时长（秒），`0` 表示每账号只执行一次 |
| `args` | strings | 额外透传参数 |

示例（`benchmark.conf`）：

```conf
#!stress
# caseName   errorBreak  prefix    start  end    batch  qps   runTime
login_bench  false       test_     1      1001   50     50    60
query_bench  false       test_     1      1001   100    100   60
```

#### QPS 令牌桶行为说明

压测模式使用 **连续时间浮点令牌桶**，动态计算等待间隔（无固定 tick 粒度）：

- 令牌按连续时间累积：`tokensToAdd = elapsed.Seconds() × targetQPS`，无整数 tick 截断
- 等待间隔根据缺额精确计算：`waitTime = (1 - tokens) / targetQPS`，最小休眠 1ms
- 桶容量上限 1.5 个令牌，即使长时间卡顿后也不会累积大量令牌导致 QPS 突发
- `target_qps=60` 可精确达到 60 QPS（旧版 20ms tick 仅能达到 ~50）

#### 交互模式执行用例

```
run-case <case_name> <openid_prefix> <user_count> <batch_count> <iterations> [args...]
run-case-stress <caseName> <errorBreak> <openIdPrefix> <idStart> <idEnd> <batchCount> <targetQPS> <runTime> [args...]
```

## 完整使用示例

以下展示一个完整的项目结构（参考实际游戏服务器测试客户端）：

```
your-robot/
├── main.go                # 入口：初始化配置、实现 UnpackMessage、调用 StartRobot
├── go.mod
├── case/
│   └── basic_case.go      # 注册用例：login, logout, gm, delay_second 等
├── case_config/
│   └── simple_test.conf   # 用例配置文件
├── cmd/
│   ├── user.go            # 用户命令：login, logout, getInfo, ping, gm
│   ├── adventure.go       # 业务命令：冒险相关
│   └── ...                # 更多业务命令
├── protocol/
│   ├── user.go            # RPC 封装：LoginAuthRpc, LoginRpc, GetInfoRpc
│   ├── rpc_handle.go      # 生成的 RPC 处理器（Send* / RegisterMessageHandler*）
│   └── ...                # 更多 RPC 封装
└── task/
    └── user.go            # 任务定义：LoginTask, LogoutTask, PingTask
```

### main.go 示例

```go
package main

import (
    "fmt"
    "os"

    "google.golang.org/protobuf/proto"

    _ "your-project/case"      // 通过 init() 自动注册用例
    _ "your-project/cmd"       // 通过 init() 自动注册命令
    robot "github.com/atframework/robot-go"
)

func UnpackMessage(msg proto.Message) (rpcName string, typeName string, errorCode int32,
    msgHead proto.Message, bodyBin []byte, sequence uint64, err error) {
    csMsg, ok := msg.(*YourCSMsg)
    if !ok {
        err = fmt.Errorf("message type invalid: %T", msg)
        return
    }
    // 从 csMsg 中提取 rpcName, errorCode, bodyBin, sequence 等
    return
}

func main() {
    flagSet := robot.NewRobotFlagSet()
    if err := flagSet.Parse(os.Args[1:]); err != nil {
        fmt.Println(err)
        return
    }

    robot.StartRobot(flagSet, UnpackMessage, func() proto.Message {
        return &YourCSMsg{}
    })
}
```

### task 示例

```go
func LoginTask(task *user_data.TaskActionUser) error {
    // 1. 登录认证
    errCode, rsp, err := protocol.LoginAuthRpc(task)
    if err != nil {
        return err
    }

    user := task.User
    user.SetLoginCode(rsp.GetLoginCode())
    user.SetUserId(rsp.GetUserId())

    // 2. 登录
    errCode, loginRsp, err := protocol.LoginRpc(task)
    if err != nil {
        return err
    }

    user.SetLogined(true)
    user.SetHeartbeatInterval(time.Duration(loginRsp.GetHeartbeatInterval()) * time.Second)
    user.InitHeartbeatFunc(PingTask)
    return nil
}
```

## 运行

```bash
# 交互模式
go run . -url ws://localhost:7001/ws/v1

# 执行普通用例文件
go run . -url ws://localhost:7001/ws/v1 -case_file case_config/simple_test.conf

# 执行压测用例（单机，含报告）
go run . -url ws://localhost:7001/ws/v1 \
    -case_file case_config/benchmark.conf \
    -report-dir ./report

# 使用 YAML 配置文件（所有参数均可写入 yaml）
go run . -config case_config/my_config.yaml
```

## YAML 配置文件

所有命令行参数均可写入 YAML 文件，通过 `-config` 指定（默认自动读取当前目录下的 `config.yaml`）：

```yaml
# config.yaml 示例
url: ws://192.168.1.2:7001/ws/v1
connect-type: websocket
case_file: case_config/benchmark.conf
case_file_repeated: 1
report-dir: ../report
```

命令行参数优先级 > YAML 文件 > 默认值。

## 报告系统

启用 `-report-dir` 后，运行结束时在指定目录生成：

- `<report_id>.json` — 原始数据（Tracings、Metrics、Pressure）
- `<report_id>.html` — ECharts 可视化报告（无需网络，CDN 自动降级）

报告内容包含：
- 分用例统计表（Total / Avg / P50 / P90 / P99 / Min / Max / 成功率）
- 每个用例的 QPS 曲线（折线图，可图例切换显示/隐藏）
- 延迟分布曲线（P50/P90/P99）
- 成功/失败趋势
- 错误码分布饼图（如有）
- 在线用户及自定义 Metrics 曲线

## 分布式压测（Master / Agent）

当单台机器无法产生足够压力时，可使用 Master/Agent 模式将压测任务分发到多台机器执行，Redis 作为数据汇聚中间件。

```
┌──────────┐       HTTP API       ┌──────────────┐
│  Master  │ ←─────────────────→ │   Agent 1    │
│  :8080   │                      │  :8081       │
│          │                      ├──────────────┤
│  Redis   │ ←─  写入 Tracings  ─ │   Agent 2    │
│          │                      │  :8082       │
└──────────┘                      └──────────────┘
```

### 一键部署 Master（Release 下载）

Master 提供独立预编译二进制，无需 Go 环境和业务 protobuf 依赖，仅需 Redis 即可启动。
Release 中包含默认配置 `master.yaml`，部署脚本会自动下载二进制和配置文件。

#### 一键部署脚本（推荐）

**Linux / macOS：**

```bash
# 下载并执行部署脚本（默认安装到 ./robot-master/ 目录）
curl -fsSL https://raw.githubusercontent.com/atframework/robot-go/main/deploy-master.sh | bash

# 或指定版本和安装目录
bash deploy-master.sh -v v1.0.0 -d /opt/robot-master

# 启动 Master
cd robot-master
./robot-master -config master.yaml
```

**Windows PowerShell：**

```powershell
# 下载并执行部署脚本
irm https://raw.githubusercontent.com/atframework/robot-go/main/deploy-master.ps1 -OutFile deploy-master.ps1
.\deploy-master.ps1

# 或指定版本和安装目录
.\deploy-master.ps1 -Version v1.0.0 -Dir C:\robot-master

# 启动 Master
cd robot-master
.\robot-master.exe -config master.yaml
```

部署脚本会自动：
1. 检测当前平台（OS/Arch）
2. 从 GitHub Releases 下载对应二进制
3. 下载默认配置 `master.yaml`（已有配置不覆盖）

#### 手动下载

```bash
# 以 linux-amd64 为例
curl -fSL "https://github.com/atframework/robot-go/releases/latest/download/robot-master-linux-amd64" -o robot-master
curl -fSL "https://github.com/atframework/robot-go/releases/latest/download/master.yaml" -o master.yaml
chmod +x robot-master
./robot-master -config master.yaml
```

支持的平台：`linux-amd64`、`linux-arm64`、`darwin-amd64`、`darwin-arm64`、`windows-amd64`。

> **GitHub Actions CI**：对仓库打 `v*` Tag 后自动构建所有平台二进制并发布到 GitHub Releases。

### 快速启动（源码编译模式）

**1. 启动 Master：**

```bash
go run . -mode master \
    -listen :8080 \
    -redis-addr localhost:6379 \
    -report-dir ../report
```

**2. 在每台压测机上启动 Agent：**

```bash
# Agent 1（机器 A）
go run . -mode agent \
    -master-addr http://192.168.1.10:8080 \
    -redis-addr 192.168.1.10:6379 \
    -url ws://192.168.1.2:7001/ws/v1 \
    -agent-id agent-01

# Agent 2（机器 B）
go run . -mode agent \
    -master-addr http://192.168.1.10:8080 \
    -redis-addr 192.168.1.10:6379 \
    -url ws://192.168.1.2:7001/ws/v1 \
    -agent-id agent-02
```

推荐使用 YAML 配置文件管理参数（见 `case_config/master_config.yaml` 和 `case_config/agent_config.yaml`）。

### Master HTTP API

| Method | Path | 说明 |
|--------|------|------|
| `POST` | `/api/agents/register` | Agent 注册（Agent 启动时自动调用） |
| `GET` | `/api/agents` | 查询已注册的 Agent 列表及状态 |
| `POST` | `/api/tasks` | 提交压测任务（返回 `report_id`，异步执行） |
| `GET` | `/api/tasks/{id}` | 查询任务状态（`pending/running/done/error`） |
| `GET` | `/api/reports` | 列出所有报告 |
| `POST` | `/api/reports/{id}/html` | 从 Redis 数据重新生成 HTML 报告 |

**提交压测任务示例：**

```bash
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "case_file_content": "#!stress\nlogin_bench false test_ 1 1001 50 50 60",
    "repeated_time": 1,
    "report_id": "bench-20250601"
  }'
```

### 任务执行流程

1. Master 接收任务，解析 `#!stress` 内容中的每个压测行
2. 将账号 ID 范围（`[id_start, id_end)`）均匀拆分给各 Agent
3. 每个 Agent 独立执行 `RunCaseStress`，将 Tracer 数据写入 Redis
4. 所有 Agent 完成后，Master 从 Redis 聚合数据，生成 HTML 报告



[MIT License](LICENSE)
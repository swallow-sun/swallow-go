---
AIGC:
  ContentProducer: '001191110102MAD55U9H0F10002'
  ContentPropagator: '001191110102MAD55U9H0F10002'
  Label: '1'
  ProduceID: 'cd106f15-29e4-4e6a-a882-fbd47850f64a'
  PropagateID: 'cd106f15-29e4-4e6a-a882-fbd47850f64a'
  ReservedCode1: 'b81d51fb-3637-4056-8938-a6038c80f5b9'
  ReservedCode2: 'b81d51fb-3637-4056-8938-a6038c80f5b9'
---

# Swallow 项目配置文档

## 一、项目概述

Swallow 是一个基于 Go 语言的 Web 服务项目，使用字节跳动开源的高性能 HTTP 框架 Hertz 构建。项目通过官方脚手架工具 hz 生成标准骨架，日志系统已替换为 uber-go/zap，并配置了 Makefile 用于快速构建与启动。

### 技术栈概览

| 组件 | 名称 | 版本 | 说明 |
|------|------|------|------|
| 语言 | Go | 1.25.12 | 编程语言 |
| Web 框架 | Hertz | v0.10.6 | 字节跳动 CloudWeGo 开源 HTTP 框架 |
| 脚手架 | hz | v0.9.7 | Hertz 官方代码生成工具 |
| 日志库 | zap | v1.23.0 | uber-go 高性能结构化日志 |
| 日志适配 | hertz-contrib/logger/zap | v1.1.0 | zap 与 Hertz hlog 的桥接层 |
| 构建工具 | GNU Make | 4.4.1 | 命令行构建自动化 |
| C/C++ 编译器 | GCC / G++ | 15.2.0 | MinGW-w64（通用编译环境） |

---

## 二、环境要求

### 2.1 Go 环境

- Go 版本：1.25+
- GOPATH：`C:\Users\redmi\go`
- GOROOT：`C:\Program Files\Go`
- 代理配置：`GOPROXY=https://goproxy.cn,direct`（国内加速）

### 2.2 构建工具

以下工具通过 scoop 包管理器安装，位于 `C:\Users\redmi\scoop\shims\`：

| 工具 | 安装来源 | 用途 |
|------|----------|------|
| make | scoop install make | 构建命令 |
| gcc / g++ | scoop install gcc | C/C++ 编译 |

**注意**：安装 scoop 后，新打开的终端窗口才能识别 make、gcc、g++ 命令。若当前终端无法识别，可执行以下命令临时刷新 PATH：

```
$env:PATH = [Environment]::GetEnvironmentVariable("PATH", "User") + ";" + [Environment]::GetEnvironmentVariable("PATH", "Machine")
```

---

## 三、项目结构

```
D:\swallow\
├── main.go              # 程序入口，初始化日志并启动 Hertz 服务
├── router.go            # 自定义路由注册（手动维护）
├── router_gen.go        # 自动生成的路由总入口（勿手动修改）
├── go.mod               # Go 模块定义与依赖声明
├── go.sum               # 依赖校验文件
├── Makefile             # 构建命令配置
├── .hz                  # hz 脚手架配置文件
├── .gitignore           # Git 忽略规则
└── biz\
    ├── handler\
    │   └── ping.go      # Ping handler，返回 {"message":"pong"}
    └── router\
        └── register.go  # IDL 自动生成的路由注册入口（勿手动修改）
```

### 文件职责说明

**main.go** — 程序入口

- 调用 `hlog.SetLogger()` 将全局日志器替换为 zap
- 创建 Hertz 实例（`server.Default()`）
- 调用 `register(h)` 注册全部路由
- 调用 `h.Spin()` 启动服务，默认监听 `:8888`

**router.go** — 自定义路由

- 定义 `customizedRegister()` 函数，手动注册业务路由
- 当前已注册 `GET /ping`
- 新增路由在此文件中添加

**router_gen.go** — 路由总入口（自动生成，勿修改）

- 定义 `register()` 函数，依次调用 `GeneratedRegister()` 和 `customizedRegister()`
- 将 IDL 生成的路由与手动路由统一挂载到 Hertz 实例

**biz/handler/ping.go** — 示例 Handler

- 实现 `Ping()` 函数，返回 `{"message":"pong"}`
- 作为新 Handler 的编写参考模板

**biz/router/register.go** — IDL 路由注册（自动生成，勿修改）

- 定义 `GeneratedRegister()` 函数
- 使用 IDL（Thrift/Protobuf）生成代码时，`hz` 会将路由注册到此
- 当前无 IDL，内含 `//INSERT_POINT` 占位标记

---

## 四、依赖配置

### 4.1 核心依赖

go.mod 中声明的直接依赖：

| 依赖 | 版本 | 用途 |
|------|------|------|
| github.com/cloudwego/hertz | v0.10.6 | Web 框架 |
| github.com/hertz-contrib/logger/zap | v1.1.0 | zap 日志适配（当前标记为 indirect，实际被 main.go 引用） |

### 4.2 主要间接依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| github.com/bytedance/sonic | v1.15.0 | 高性能 JSON 序列化 |
| github.com/cloudwego/netpoll | v0.7.5 | 网络库（Hertz 底层传输） |
| go.uber.org/zap | v1.23.0 | 结构化日志核心库 |
| google.golang.org/protobuf | v1.34.1 | Protobuf 支持 |
| github.com/fsnotify/fsnotify | v1.5.4 | 文件变更通知 |

### 4.3 依赖管理命令

```
go mod tidy     # 整理依赖，添加缺失、移除多余
go mod download # 下载依赖到本地缓存
go mod graph    # 查看依赖关系图
```

---

## 五、日志配置

### 5.1 当前配置

项目已将 Hertz 默认日志替换为 zap，配置位于 `main.go`：

```go
hlog.SetLogger(zaplog.NewLogger())
```

通过 `hlog.SetLogger()` 全局替换后，Hertz 内部所有日志输出（启动信息、路由注册、请求日志、错误日志等）均通过 zap 输出。

### 5.2 自定义 zap 选项

如需自定义 zap 配置（如输出格式、日志级别、输出到文件等），可通过 `NewLogger()` 的 Option 参数实现：

```go
import (
    "go.uber.org/zap"
    zaplog "github.com/hertz-contrib/logger/zap"
)

// 示例：带调用者信息的 zap 日志
hlog.SetLogger(zaplog.NewLogger(
    zaplog.WithZapOptions(zap.AddCaller()),
))

// 示例：同步写文件 + 控制台双输出
// 需结合 WithCores 选项配置
```

### 5.3 可用配置选项

| 选项函数 | 作用 |
|----------|------|
| `WithZapOptions(opts ...zap.Option)` | 透传原生 zap.Option |
| `WithCoreLevel(lvl zap.AtomicLevel)` | 设置日志级别 |
| `WithCoreEnc(enc zapcore.Encoder)` | 设置编码器（JSON/Console） |
| `WithCoreWs(ws zapcore.WriteSyncer)` | 设置输出目标 |
| `WithCores(coreConfigs ...CoreConfig)` | 多核心输出（如同时写文件和控制台） |
| `WithExtraKeys(keys []ExtraKey) ` | 附加上下文字段 |

---

## 六、Makefile 命令

| 命令 | 作用 |
|------|------|
| `make run` | 直接启动服务（go run .） |
| `make build` | 编译为 swallow.exe 二进制文件 |
| `make tidy` | 整理 Go 依赖（go mod tidy） |
| `make clean` | 清理编译产物 |

### 使用方式

```
cd D:\swallow
make run     # 启动服务
make build   # 编译
make clean   # 清理
```

---

## 七、服务运行

### 7.1 启动方式

方式一：Make

```
make run
```

方式二：直接运行

```
go run .
```

### 7.2 默认配置

| 配置项 | 默认值 |
|--------|--------|
| 监听地址 | 0.0.0.0:8888 |
| 网络库 | netpoll |
| JSON 序列化 | bytedance/sonic |

### 7.3 接口验证

启动服务后访问 ping 接口：

```
curl http://localhost:8888/ping
```

返回结果：

```json
{"message":"pong"}
```

---

## 八、开发指南

### 8.1 新增路由

1. 在 `biz/handler/` 下新建 handler 文件（参考 `ping.go`）
2. 在 `router.go` 的 `customizedRegister()` 中注册路由

示例：

```go
// biz/handler/hello.go
package handler

import (
    "context"
    "github.com/cloudwego/hertz/pkg/app"
    "github.com/cloudwego/hertz/pkg/protocol/consts"
)

func Hello(ctx context.Context, c *app.RequestContext) {
    c.JSON(consts.StatusOK, map[string]string{
        "message": "hello",
    })
}
```

```go
// router.go 中添加
r.GET("/hello", handler.Hello)
```

### 8.2 新增依赖

```
go get github.com/xxx/xxx@latest
go mod tidy
```

### 8.3 使用 IDL 生成代码

若需通过 IDL（Thrift/Protobuf）自动生成路由和 handler 骨架：

```
hz update -idl api.thrift
```

生成的路由会自动注册到 `biz/router/register.go` 的 `GeneratedRegister()` 中。

---

## 九、环境变量

| 变量 | 值 | 说明 |
|------|----|------|
| GOPROXY | https://goproxy.cn,direct | Go 模块代理（国内加速） |
| GOPATH | C:\Users\redmi\go | Go 工作目录 |
| GOROOT | C:\Program Files\Go | Go 安装目录 |

---

## 十、注意事项

1. `router_gen.go` 和 `biz/router/register.go` 标注了 `DO NOT EDIT`，不要手动修改，`hz` 重新生成时会覆盖
2. 自定义路由统一写在 `router.go` 的 `customizedRegister()` 中
3. `.gitignore` 已配置忽略 `*.exe`，编译产物不会被提交
4. 新终端窗口执行 make 前需确认 scoop 路径已在 PATH 中
5. kill 服务可通过查找 8888 端口占用进程并终止

> AI生成

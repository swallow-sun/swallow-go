# Swallow-Go

## 技术栈

- Go 1.25 + Hertz（HTTP 框架）+ GORM（ORM）+ SQLite
- zap（结构化日志，JSON 格式输出到 stdout）
- TOML 配置（config.toml + config.local.toml 覆盖）
- AES-256-GCM 加密存储敏感配置

## 快速开始

```bash
# 1. 复制配置文件，填入 DeepSeek API Key
cp config.toml config.local.toml
# 编辑 config.local.toml，填入 api_key 和 owner_token

# 2. 启动 HTTP 服务（首次启动自动建表 + 迁移）
make run

# 3. 创建主人会话
curl -X POST http://localhost:8888/api/session \
  -H "Authorization: Bearer <owner_token>" \
  -H "Content-Type: application/json" \
  -d '{"user_name":"owner"}'

# 4. 发消息（SSE 流式返回）
curl -X POST http://localhost:8888/api/chat \
  -H "Authorization: Bearer <owner_token>" \
  -H "Content-Type: application/json" \
  -d '{"session_id":"<上一步返回的ID>","client_message_id":"<UUID>","message":"你好"}'

# 5. 查历史消息
curl "http://localhost:8888/api/history?session_id=<ID>" \
  -H "Authorization: Bearer <owner_token>"
```

也可以用 CLI 对话：`make chat`

## 项目结构

```
swallow-go/
├── cmd/
│   ├── server/          # HTTP 服务入口
│   └── chat/           # CLI 对话入口
├── biz/                 # 业务层（HTTP 边界）
│   ├── handler/         # 请求处理（一个接口一个文件）
│   ├── service/         # 业务逻辑（不依赖 HTTP 框架）
│   └── router/          # 路由注册
├── internal/            # 内部模块
│   ├── agent/           # LLM 调用代理（流式 + 非流式）
│   ├── config/          # TOML 配置加载
│   ├── data/            # 数据层（GORM + SQLite，Repository 接口）
│   ├── debug/           # pprof 调试服务
│   ├── device/          # 设备注册、令牌生成与设备身份认证
│   ├── identity/        # 身份识别
│   ├── memory/         # 短期记忆（对话上下文）
│   ├── provider/llm/    # LLM 供应商（OpenAI 兼容协议）
│   ├── settings/        # 加密配置服务（AES-256-GCM）
│   ├── telemetry/      # 埋点系统（异步 channel + 写库）
│   └── trace/          # 链路追踪 ID（context 传递）
├── pkg/logger/         # 统一日志（zap 封装 + Hertz/GORM 适配器）
├── script/migrations/  # 版本化 SQL 迁移文件
├── prompts/            # 系统提示词（Markdown）
├── config.toml         # 配置文件
└── Makefile
```

## 架构

三层分离：handler → service → data/agent

- **handler**：HTTP 边界，解析请求、返回响应，一个接口一个文件
- **service**：业务逻辑，不依赖 HTTP 框架，ChatService 通过 event channel 与 handler 解耦
- **data**：Repository 接口 + SQLite 实现，换数据库只需换实现
- **agent**：LLM 调用代理，封装流式 SSE 和非流式调用

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/ping` | 健康检查 |
| POST | `/api/session` | 创建会话 |
| GET | `/api/history?session_id=` | 查历史消息 |
| POST | `/api/chat` | 发消息对话（SSE 流式） |
| POST | `/api/v1/devices/register` | 使用主人令牌注册设备，设备令牌只返回一次 |
| GET | `/api/v1/devices/me` | 使用设备令牌查询当前设备身份 |
| POST | `/api/v1/device/session` | 使用设备令牌创建对话会话 |
| POST | `/api/v1/device/chat` | 使用设备令牌调用 Go 云端模型（SSE 流式） |
| GET | `/api/v1/memory-candidates` | 查询长期记忆候选 |
| GET | `/api/v1/memories` | 查询正式长期记忆 |
| GET | `/api/v1/dashboard/model-usage` | 查询模型用量看板数据 |

## 数据库

SQLite + WAL 模式，版本化迁移（`script/migrations/`）：

| 版本 | 文件 | 说明 |
|---|---|---|
| 1 | 0001_init | users / sessions / dialogues / events |
| 2 | 0002_chat_requests | chat_requests（幂等） |
| 3 | 0003_runtime_settings | app_settings / encrypted_secrets |
| 4 | 0004_model_usages | model_usages（Token 用量 + 费用估算） |
| 5 | 0005_create_spans | spans（调用链步骤） |
| 6 | 0006_create_model_price_snapshots | 模型价格快照 |
| 7 | 0007_create_model_usage_daily | 模型用量日聚合 |
| 8 | 0008_create_memory_tables | 长期记忆候选、正式记忆、版本和删除墓碑 |
| 9 | 0009_fix_model_usage_daily_unique | 修复模型用量日聚合唯一索引 |
| 10 | 0010_users_name_unique | 用户名唯一索引 |
| 11 | 0011_create_devices | 设备身份、令牌摘要、状态和最近在线时间 |

迁移铁律：已执行的迁移文件不可修改，要改表结构必须新写更高版本的迁移文件。

## 日志

- 统一 JSON 格式输出到 stdout
- 开发环境 Debug 起步，生产环境 Info 起步
- 时间用 ISO8601 带时区
- GORM 每条 SQL 转发到 logger.Debug
- Hertz 框架日志固定 Info 级别（过滤路由注册噪音）
- 所有日志带 startup_id，业务日志带 trace_id

## 配置

`config.toml` 是基础配置，`config.local.toml` 覆盖敏感字段（不提交 Git）。

敏感配置（API Key 等）首次启动后自动加密写入数据库，后续从数据库解密读取。

## 开发

```bash
make run      # 启动 HTTP 服务
make chat     # CLI 对话
make build    # 编译
make test     # 运行测试
make vet      # 静态检查
make check    # 完整质量检查（test + vet + build）
make tidy     # 整理依赖
make clean    # 清理构建产物
```

pprof 调试：在 `config.local.toml` 里设 `pprof_port = 6060`，然后 `go tool pprof http://localhost:6060/debug/pprof/heap`。

-- Swallow-Go SQLite 数据库初始化脚本
--
-- 用途：数据库完整结构快照，供人工审查和排障。
-- 应用运行时以 script/migrations 下的版本化 SQL 为唯一迁移来源。
-- 修改结构时必须新增迁移文件，不得修改已经执行过的迁移文件。

PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

-- users：保存助手识别和服务的用户身份。
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT, -- 用户内部自增主键
    name TEXT NOT NULL,                   -- 用户显示名；当前开发阶段也用于查找用户
    role TEXT DEFAULT 'owner',            -- 用户角色；当前默认 owner
    voice_print TEXT,                     -- 预留声纹特征或声纹引用
    face_print TEXT,                      -- 预留人脸特征或人脸引用
    created_at DATETIME,                  -- 用户创建时间
    last_active_at DATETIME               -- 用户最后活跃时间
);

-- sessions：保存一次连续对话会话，并关联所属用户。
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,          -- 客户端后续聊天和查询历史使用的 UUID
    user_id INTEGER NOT NULL,     -- 会话所属用户 ID；不建立数据库外键
    started_at DATETIME,          -- 会话开始时间
    last_active_at DATETIME,      -- 会话最后活跃时间
    status TEXT DEFAULT 'active'  -- 会话状态，例如 active
);

-- dialogues：保存用户和助手的完整对话正文，以及助手回复的 Token 摘要。
CREATE TABLE IF NOT EXISTS dialogues (
    id INTEGER PRIMARY KEY AUTOINCREMENT, -- 对话消息自增主键
    session_id TEXT NOT NULL,             -- 消息所属会话 ID
    user_id INTEGER NOT NULL,             -- 消息所属用户 ID
    role TEXT NOT NULL,                   -- 消息角色：user 或 assistant
    content TEXT NOT NULL,                -- 消息完整文本
    prompt_tokens INTEGER DEFAULT 0,      -- 模型输入 Token 数
    completion_tokens INTEGER DEFAULT 0,  -- 模型输出 Token 数
    cache_hit_tokens INTEGER DEFAULT 0,   -- 输入中命中供应商缓存的 Token 数
    cache_miss_tokens INTEGER DEFAULT 0,  -- 输入中未命中缓存的 Token 数
    reasoning_tokens INTEGER DEFAULT 0,   -- 供应商返回的推理 Token 数
    total_tokens INTEGER DEFAULT 0,       -- 本次调用总 Token 数
    trace_id TEXT,                        -- 关联日志、事件和请求的链路 ID
    timestamp DATETIME                    -- 消息保存时间
);

-- events：保存异步遥测事件，用于性能、错误和运行看板。
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT, -- 事件自增主键
    event_type TEXT NOT NULL,             -- 稳定事件类型，例如 memory_query
    user_id INTEGER,                      -- 关联用户 ID；当前部分事件可能为空
    event_data TEXT,                      -- JSON 格式的事件扩展数据
    trace_id TEXT,                        -- 关联同一请求的链路 ID
    timestamp DATETIME,                   -- 事件发生时间
    duration_ms INTEGER,                  -- 事件耗时，单位毫秒
    success INTEGER DEFAULT 1             -- 是否成功：1 成功，0 失败
);

-- chat_requests：记录一轮 HTTP 聊天的幂等状态，避免重试时重复调用模型和计费。
CREATE TABLE IF NOT EXISTS chat_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT, -- 聊天请求内部自增主键
    client_message_id TEXT NOT NULL,      -- 客户端生成的消息 ID；同一消息重试必须复用
    session_id TEXT NOT NULL,             -- 请求所属会话 ID，与 client_message_id 组成唯一键
    user_id INTEGER NOT NULL,             -- 请求所属用户 ID
    status TEXT NOT NULL,                 -- accepted、running、completed 或 failed
    user_dialogue_id INTEGER,             -- 已保存用户消息在 dialogues 表中的 ID
    assistant_dialogue_id INTEGER,        -- 已保存助手消息 ID；完成结果重放时使用
    error_code TEXT,                      -- 请求失败时保存的稳定错误码
    trace_id TEXT NOT NULL,               -- 关联本轮日志、事件和对话的链路 ID
    created_at DATETIME,                  -- 请求首次被接受的时间
    updated_at DATETIME,                  -- 请求状态最后更新时间
    completed_at DATETIME                 -- 请求成功完成时间；未完成时为空
);

-- app_settings：保存模型名称、服务地址等不敏感运行配置。
CREATE TABLE IF NOT EXISTS app_settings (
    setting_key TEXT PRIMARY KEY, -- 稳定配置键，例如 llm.model
    setting_value TEXT NOT NULL,  -- 普通配置值；禁止保存密钥和令牌
    value_type TEXT NOT NULL,     -- 值类型；当前支持 string
    description TEXT,             -- 配置用途的中文说明
    updated_at DATETIME NOT NULL  -- 配置最后更新时间
);

-- encrypted_secrets：保存 API Key 和 owner token 的 AES-256-GCM 密文。
CREATE TABLE IF NOT EXISTS encrypted_secrets (
    secret_key TEXT PRIMARY KEY, -- 稳定密钥名
    ciphertext BLOB NOT NULL,    -- 带认证标签的密文
    nonce BLOB NOT NULL,         -- 每次加密随机生成的 nonce
    algorithm TEXT NOT NULL,     -- 当前固定 aes-256-gcm
    key_version INTEGER NOT NULL,-- 主密钥版本
    updated_at DATETIME NOT NULL -- 密文最后更新时间
);

-- 获取某个会话最近的消息，并在相同时间戳下使用 ID 保证稳定排序。
CREATE INDEX IF NOT EXISTS idx_dialogues_session_time
    ON dialogues(session_id, timestamp DESC, id DESC);

-- 按事件类型和时间查询埋点。
CREATE INDEX IF NOT EXISTS idx_events_type_time
    ON events(event_type, timestamp DESC);

-- 使用 trace_id 关联一次请求产生的对话、事件和日志。
CREATE INDEX IF NOT EXISTS idx_dialogues_trace
    ON dialogues(trace_id);

CREATE INDEX IF NOT EXISTS idx_events_trace
    ON events(trace_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_requests_session_client
    ON chat_requests(session_id, client_message_id);

CREATE INDEX IF NOT EXISTS idx_chat_requests_trace
    ON chat_requests(trace_id);

CREATE INDEX IF NOT EXISTS idx_chat_requests_status_updated
    ON chat_requests(status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_app_settings_updated
    ON app_settings(updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_encrypted_secrets_key_version
    ON encrypted_secrets(key_version, updated_at DESC);

-- model_usages：逐次记录模型调用的用量、状态、耗时和费用估算。
CREATE TABLE IF NOT EXISTS model_usages (
    id INTEGER PRIMARY KEY AUTOINCREMENT, -- 模型调用记录主键
    request_id TEXT,                      -- 内部请求标识
    trace_id TEXT NOT NULL,               -- 链路追踪 ID
    session_id TEXT,                      -- 来源会话 ID
    user_id INTEGER,                      -- 来源用户 ID
    device_id TEXT,                       -- 来源设备 ID；未关联设备时为空
    provider TEXT NOT NULL,               -- 模型供应商
    model TEXT NOT NULL,                  -- 模型名称
    operation TEXT NOT NULL,              -- chat、embedding、vision、asr 或 tts
    input_tokens INTEGER,                 -- 输入 Token 数
    output_tokens INTEGER,                -- 输出 Token 数
    cached_input_tokens INTEGER,          -- 缓存命中输入 Token 数
    cache_miss_tokens INTEGER,            -- 缓存未命中输入 Token 数
    cache_creation_tokens INTEGER,        -- 创建缓存使用的 Token 数
    reasoning_tokens INTEGER,             -- 推理 Token 数
    total_tokens INTEGER,                 -- 总 Token 数
    input_audio_seconds REAL,             -- ASR 输入音频秒数
    output_audio_seconds REAL,            -- TTS 输出音频秒数
    input_image_count INTEGER,            -- 视觉输入图片数
    currency TEXT,                        -- 费用币种
    estimated_cost_micros INTEGER,        -- 百万分之一货币单位的估算费用
    provider_request_id TEXT,             -- 供应商请求 ID
    status TEXT NOT NULL,                 -- ok 或 failed
    duration_ms INTEGER,                  -- 调用耗时，单位毫秒
    occurred_at DATETIME NOT NULL         -- 调用发生时间
);

CREATE INDEX IF NOT EXISTS idx_model_usages_trace ON model_usages(trace_id);
CREATE INDEX IF NOT EXISTS idx_model_usages_provider_model ON model_usages(provider, model, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usages_time ON model_usages(occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usages_user ON model_usages(user_id, occurred_at DESC);

-- spans：记录一次请求中各处理步骤，parent_span_id 用于还原调用树。
CREATE TABLE IF NOT EXISTS spans (
    id TEXT PRIMARY KEY,           -- Span UUID
    trace_id TEXT NOT NULL,        -- 整条调用链共享的 Trace ID
    parent_span_id TEXT,           -- 父 Span ID；根节点为空
    component TEXT NOT NULL,       -- handler、service 或 provider 等组件
    operation TEXT NOT NULL,       -- 当前步骤执行的操作
    status TEXT NOT NULL,          -- ok、error 或 cancelled
    duration_ms INTEGER DEFAULT 0, -- 步骤耗时，单位毫秒
    started_at DATETIME NOT NULL,  -- 开始时间
    finished_at DATETIME,          -- 结束时间
    attributes TEXT                -- JSON 扩展属性；不得写入密钥和对话正文
);

CREATE INDEX IF NOT EXISTS idx_spans_trace ON spans(trace_id, started_at ASC);
CREATE INDEX IF NOT EXISTS idx_spans_parent ON spans(parent_span_id);

-- model_price_snapshots：保存模型价格版本，避免用新价格重算历史费用。
CREATE TABLE IF NOT EXISTS model_price_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT, -- 价格快照主键
    provider TEXT NOT NULL,               -- 供应商
    model TEXT NOT NULL,                  -- 模型名称
    effective_from DATETIME NOT NULL,     -- 价格生效时间
    input_price REAL,                     -- 每百万输入 Token 单价
    output_price REAL,                    -- 每百万输出 Token 单价
    cached_input_price REAL,              -- 每百万缓存命中 Token 单价
    cache_creation_price REAL,            -- 每百万缓存创建 Token 单价
    unit TEXT NOT NULL,                   -- 计价单位
    currency TEXT NOT NULL,               -- 币种
    source_version TEXT,                  -- 价格来源版本
    created_at DATETIME NOT NULL          -- 快照创建时间
);

CREATE INDEX IF NOT EXISTS idx_price_snapshots_provider_model_time ON model_price_snapshots(provider, model, effective_from DESC);
CREATE INDEX IF NOT EXISTS idx_price_snapshots_provider_model ON model_price_snapshots(provider, model, created_at DESC);

-- model_usage_daily：看板使用的日聚合表；空设备和系统用户使用明确零值参与唯一键。
CREATE TABLE IF NOT EXISTS model_usage_daily (
    id INTEGER PRIMARY KEY AUTOINCREMENT,             -- 聚合记录主键
    date TEXT NOT NULL,                               -- 聚合日期，格式 YYYY-MM-DD
    device_id TEXT NOT NULL DEFAULT '',               -- 设备 ID；空字符串表示未关联设备
    user_id INTEGER NOT NULL DEFAULT 0,               -- 用户 ID；0 表示系统级调用
    provider TEXT NOT NULL,                           -- 供应商
    model TEXT NOT NULL,                              -- 模型名称
    operation TEXT NOT NULL,                          -- 模型操作类型
    request_count INTEGER NOT NULL DEFAULT 0,         -- 请求总数
    failed_count INTEGER NOT NULL DEFAULT 0,          -- 失败请求数
    input_tokens INTEGER NOT NULL DEFAULT 0,          -- 输入 Token 总数
    output_tokens INTEGER NOT NULL DEFAULT 0,         -- 输出 Token 总数
    cached_input_tokens INTEGER NOT NULL DEFAULT 0,   -- 缓存命中 Token 总数
    estimated_cost_micros INTEGER,                    -- 估算费用总数
    currency TEXT                                     -- 费用币种
);

CREATE INDEX IF NOT EXISTS idx_usage_daily_date ON model_usage_daily(date DESC);
CREATE INDEX IF NOT EXISTS idx_usage_daily_provider_model ON model_usage_daily(provider, model, date DESC);
CREATE INDEX IF NOT EXISTS idx_usage_daily_user ON model_usage_daily(user_id, date DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_daily_unique ON model_usage_daily(date, device_id, user_id, provider, model, operation);

-- memory_candidates：保存待用户确认的长期记忆候选，模型不能直接写正式记忆。
CREATE TABLE IF NOT EXISTS memory_candidates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,          -- 候选主键
    user_id INTEGER NOT NULL,                      -- 所属用户
    session_id TEXT NOT NULL,                      -- 来源会话
    trace_id TEXT,                                 -- 来源链路
    content TEXT NOT NULL,                         -- 候选内容
    memory_type TEXT NOT NULL,                     -- preference、fact、instruction 或 persona
    source TEXT NOT NULL DEFAULT 'rule',           -- rule 或 model
    reason TEXT NOT NULL DEFAULT '',               -- 建议保存的原因
    usage_hint TEXT NOT NULL DEFAULT '',            -- 保存后的使用方式
    status TEXT NOT NULL DEFAULT 'pending',         -- pending、confirmed 或 rejected
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, -- 创建时间
    resolved_at DATETIME                            -- 处理时间
);

CREATE INDEX IF NOT EXISTS idx_memory_candidates_user_status ON memory_candidates(user_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_candidates_trace ON memory_candidates(trace_id);

-- memories：只保存用户已经确认的正式长期记忆。
CREATE TABLE IF NOT EXISTS memories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,          -- 正式记忆主键
    user_id INTEGER NOT NULL,                      -- 所属用户
    candidate_id INTEGER,                          -- 来源候选；手动创建时为空
    source_session_id TEXT,                        -- 来源会话
    content TEXT NOT NULL,                         -- 记忆内容
    memory_type TEXT NOT NULL,                     -- 记忆类型
    keywords TEXT NOT NULL DEFAULT '',             -- 关键词检索文本
    sync_version INTEGER NOT NULL DEFAULT 0,       -- 设备同步版本
    status TEXT NOT NULL DEFAULT 'active',         -- active 或 deleted
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, -- 创建时间
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP  -- 更新时间
);

CREATE INDEX IF NOT EXISTS idx_memories_user ON memories(user_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_memories_user_type ON memories(user_id, memory_type, status);
CREATE INDEX IF NOT EXISTS idx_memories_user_keywords ON memories(user_id, status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_candidate_unique ON memories(candidate_id) WHERE candidate_id IS NOT NULL;

-- memory_versions：保存正式记忆的编辑历史；关联一致性由 Repository 事务保证。
CREATE TABLE IF NOT EXISTS memory_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,          -- 版本主键
    memory_id INTEGER NOT NULL,                    -- 正式记忆 ID
    version INTEGER NOT NULL,                      -- 递增版本号
    content TEXT NOT NULL,                         -- 此版本内容
    keywords TEXT NOT NULL DEFAULT '',             -- 此版本关键词
    edited_by TEXT NOT NULL DEFAULT 'user',        -- user 或 system
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP -- 版本创建时间
);

CREATE INDEX IF NOT EXISTS idx_memory_versions_memory ON memory_versions(memory_id, version DESC);

-- memory_tombstones：记录删除标记，防止已删除记忆通过设备同步重新出现。
CREATE TABLE IF NOT EXISTS memory_tombstones (
    id INTEGER PRIMARY KEY AUTOINCREMENT,          -- 删除标记主键
    memory_id INTEGER NOT NULL,                    -- 被删除的记忆 ID
    user_id INTEGER NOT NULL,                      -- 所属用户
    sync_version INTEGER NOT NULL DEFAULT 0,       -- 删除时的同步版本
    deleted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP -- 删除时间
);

CREATE INDEX IF NOT EXISTS idx_memory_tombstones_user ON memory_tombstones(user_id, deleted_at DESC);

-- memory_sync_cursors：为每个用户分配跨记忆、删除标记共享的单调同步版本。
CREATE TABLE IF NOT EXISTS memory_sync_cursors (
    user_id INTEGER PRIMARY KEY,
    next_version INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_user_sync_version ON memories(user_id, sync_version) WHERE sync_version > 0;
CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_tombstones_user_sync_version ON memory_tombstones(user_id, sync_version) WHERE sync_version > 0;
CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_versions_memory_version ON memory_versions(memory_id, version);

CREATE TABLE IF NOT EXISTS companion_states (
    user_id INTEGER PRIMARY KEY,
    concern REAL NOT NULL DEFAULT 0,
    urgency REAL NOT NULL DEFAULT 0,
    fondness REAL NOT NULL DEFAULT 0.5,
    playfulness REAL NOT NULL DEFAULT 0.3,
    allow_teasing INTEGER NOT NULL DEFAULT 1,
    allow_strict_reminder INTEGER NOT NULL DEFAULT 1,
    allow_affection INTEGER NOT NULL DEFAULT 1,
    last_mode TEXT NOT NULL DEFAULT 'neutral',
    current_task TEXT NOT NULL DEFAULT '',
    task_updated_at DATETIME,
    interaction_count INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

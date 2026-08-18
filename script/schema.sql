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

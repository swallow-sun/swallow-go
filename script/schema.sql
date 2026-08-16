-- Swallow-Go SQLite 数据库初始化脚本
--
-- 用途：数据库完整结构快照，供人工审查和排障。
-- 应用运行时以 script/migrations 下的版本化 SQL 为唯一迁移来源。
-- 修改结构时必须新增迁移文件，不得修改已经执行过的迁移文件。

PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    role TEXT DEFAULT 'owner',
    voice_print TEXT,
    face_print TEXT,
    created_at DATETIME,
    last_active_at DATETIME
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL,
    started_at DATETIME,
    last_active_at DATETIME,
    status TEXT DEFAULT 'active'
);

CREATE TABLE IF NOT EXISTS dialogues (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    cache_hit_tokens INTEGER DEFAULT 0,
    cache_miss_tokens INTEGER DEFAULT 0,
    reasoning_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    trace_id TEXT,
    timestamp DATETIME
);

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    user_id INTEGER,
    event_data TEXT,
    trace_id TEXT,
    timestamp DATETIME,
    duration_ms INTEGER,
    success INTEGER DEFAULT 1
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

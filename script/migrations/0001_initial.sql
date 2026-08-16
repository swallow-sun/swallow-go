-- Swallow-Go 初始数据库结构。
-- 业务关联只保存 ID，不创建外键约束，由业务层维护关联完整性。

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
    success NUMERIC DEFAULT true
);

CREATE INDEX IF NOT EXISTS idx_dialogues_session_time
    ON dialogues(session_id, timestamp DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_dialogues_trace ON dialogues(trace_id);
CREATE INDEX IF NOT EXISTS idx_events_type_time ON events(event_type, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_events_trace ON events(trace_id);

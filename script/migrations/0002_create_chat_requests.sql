-- 为每轮聊天增加幂等执行记录。
-- 同一会话内的 client_message_id 唯一，避免网络重试重复调用模型和重复计费。

CREATE TABLE chat_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_message_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    status TEXT NOT NULL,
    user_dialogue_id INTEGER,
    assistant_dialogue_id INTEGER,
    error_code TEXT,
    trace_id TEXT NOT NULL,
    created_at DATETIME,
    updated_at DATETIME,
    completed_at DATETIME
);

CREATE UNIQUE INDEX idx_chat_requests_session_client
    ON chat_requests(session_id, client_message_id);
CREATE INDEX idx_chat_requests_trace ON chat_requests(trace_id);
CREATE INDEX idx_chat_requests_status_updated
    ON chat_requests(status, updated_at DESC);

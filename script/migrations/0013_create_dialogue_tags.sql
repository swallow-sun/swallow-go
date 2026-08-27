-- 阶段 4.5: 用户画像、情绪感知与主动提醒.
-- 方案 16.12.6.3 节: 每轮标签明细表.
-- 每轮对话的所有标签维度各一行, 方便按维度查询和统计.
-- LLM 输出的标签 source='llm', Go 规则提取的标签 source='rule'.

CREATE TABLE dialogue_tags (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL,                    -- 哪个用户的标签
    session_id       TEXT NOT NULL,                       -- 来源会话 ID
    trace_id         TEXT,                                -- 来源对话的 trace ID
    round            INTEGER NOT NULL,                    -- 第几轮对话 (user 消息序号)
    tag_dim          TEXT NOT NULL,                       -- 维度名: emotion, communication_style, topic...
    tag_value        TEXT NOT NULL,                       -- 标签值: frustrated, direct, cpp...
    tag_extra        REAL,                                -- 数值型标签的值 (如 intensity=0.6)
    trigger_reason   TEXT NOT NULL DEFAULT '',           -- 情绪触发原因 (只有情绪维度用)
    source           TEXT NOT NULL DEFAULT 'llm',        -- 来源: llm / rule
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_dialogue_tags_user_round ON dialogue_tags(user_id, round);
CREATE INDEX idx_dialogue_tags_dim_time ON dialogue_tags(user_id, tag_dim, created_at DESC);

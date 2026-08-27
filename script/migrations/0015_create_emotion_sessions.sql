-- 阶段 4.5: 用户画像、情绪感知与主动提醒.
-- 方案 16.12.6.3 节: 情绪持续段记录.
-- 连续相同情绪合并为一段, 情绪变了就结束当前段开新段.
-- 记录开始/结束轮号、时间、持续时长, 方便画像分析时看情绪时间线.

CREATE TABLE emotion_sessions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL,                    -- 哪个用户的情绪
    emotion          TEXT NOT NULL,                       -- happy / neutral / frustrated ...
    intensity        REAL NOT NULL DEFAULT 0.5,           -- 段内平均强度
    urgency          TEXT NOT NULL DEFAULT 'normal',      -- 紧迫度
    cooperation      TEXT NOT NULL DEFAULT 'normal',      -- 配合度
    trigger          TEXT NOT NULL DEFAULT '',            -- 触发原因
    start_round      INTEGER NOT NULL,                    -- 从第几轮开始
    end_round        INTEGER,                             -- 到第几轮结束, NULL = 进行中
    start_at         DATETIME NOT NULL,                   -- 开始时间
    end_at           DATETIME,                            -- 结束时间, NULL = 进行中
    duration_minutes REAL,                               -- 持续分钟数, 结束时计算
    trace_id         TEXT,                                -- 来源对话的 trace ID
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_emotion_sessions_user ON emotion_sessions(user_id, start_at DESC);

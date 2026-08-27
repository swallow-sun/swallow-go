-- 阶段 4.5: 用户画像、情绪感知与主动提醒.
-- 方案 16.12.6.3 节: 待办提醒表.
-- 从对话中提取 (确定性规则) + 用户确认后写入.
-- 到时间后在下次对话的 system prompt 中提醒用户.

CREATE TABLE reminders (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL,                    -- 哪个用户的提醒
    session_id       TEXT NOT NULL,                       -- 来源会话
    trace_id         TEXT,                                -- 来源对话的 trace ID
    content          TEXT NOT NULL,                       -- 提醒内容, 如"交报告"
    remind_at        DATETIME NOT NULL,                   -- 触发时间
    status           TEXT NOT NULL DEFAULT 'pending',     -- pending / delivered / acknowledged / expired / cancelled
    source           TEXT NOT NULL DEFAULT 'dialogue',    -- dialogue / manual
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at     DATETIME,                            -- 投递时间
    acknowledged_at  DATETIME                              -- 确认时间
);

CREATE INDEX idx_reminders_user_status ON reminders(user_id, status, remind_at ASC);
CREATE INDEX idx_reminders_due ON reminders(status, remind_at) WHERE status = 'pending';

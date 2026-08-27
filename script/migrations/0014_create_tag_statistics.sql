-- 阶段 4.5: 用户画像、情绪感知与主动提醒.
-- 方案 16.12.6.3 节: 性格和行为维度的按天聚合计数.
-- 情绪维度不进此表, 情绪用 emotion_sessions 按持续段记录.
-- 每轮打完标签后 UPSERT 当天记录, 同一天同维度同值计数累加.

CREATE TABLE tag_statistics (
    user_id          INTEGER NOT NULL,
    tag_dim          TEXT NOT NULL,                       -- 维度名
    tag_value        TEXT NOT NULL,                      -- 标签值
    period           TEXT NOT NULL,                      -- 'YYYY-MM-DD' 按天
    hit_count        INTEGER NOT NULL DEFAULT 0,          -- 当天命中次数
    last_round       INTEGER NOT NULL DEFAULT 0,          -- 当天最近命中的轮号
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, tag_dim, tag_value, period)
);

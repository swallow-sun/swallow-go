-- 阶段 4.5: 用户画像、情绪感知与主动提醒.
-- 方案 16.12.6.3 节: 用户画像表.
-- 一个用户一条画像, profile_json 存结构化画像.
-- 每 30 轮对话后台异步触发分析, 增量更新.

CREATE TABLE user_profiles (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL UNIQUE,              -- 一个用户一条画像
    profile_json     TEXT NOT NULL DEFAULT '{}',          -- 结构化画像 JSON
    analyzed_rounds  INTEGER NOT NULL DEFAULT 0,          -- 上次分析时的总轮数
    analysis_count   INTEGER NOT NULL DEFAULT 0,          -- 分析触发次数
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

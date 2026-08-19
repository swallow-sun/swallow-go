-- 模型用量日聚合表: 看板查询用的预聚合数据, 避免每次扫描原始 model_usages 表.
-- 方案 15.7 节: 大范围查询使用预聚合表, 不能每次扫描原始事件 JSON.
-- 原始用量写入成功后通过幂等聚合任务更新日表.
-- 聚合任务失败不能丢失原始记录, 修复后可以按日期重新计算.
--
-- 字段含义:
--   date                  — 聚合日期(不含时间, 按天聚合)
--   device_id             — 设备 ID(阶段 4+ 才有, 目前 NULL)
--   user_id               — 用户 ID
--   provider              — 供应商名称
--   model                 — 模型名
--   operation             — 操作类型: chat/embedding/vision/asr/tts
--   request_count         — 请求总数
--   failed_count          — 失败请求数
--   input_tokens          — 输入 Token 总量
--   output_tokens         — 输出 Token 总量
--   cached_input_tokens   — 缓存命中输入 Token 总量
--   estimated_cost_micros — 估算费用总额(百万分之一货币单位)
--   currency              — 币种

CREATE TABLE model_usage_daily (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    date                     TEXT NOT NULL,         -- 聚合日期, 格式 YYYY-MM-DD
    device_id                TEXT,                  -- 设备 ID(阶段 4+ 才有, 目前 NULL)
    user_id                  INTEGER,               -- 用户 ID
    provider                 TEXT NOT NULL,         -- 供应商名称
    model                    TEXT NOT NULL,          -- 模型名
    operation                TEXT NOT NULL,          -- 操作类型: chat/embedding/vision/asr/tts
    request_count            INTEGER NOT NULL DEFAULT 0,  -- 请求总数
    failed_count             INTEGER NOT NULL DEFAULT 0,  -- 失败请求数
    input_tokens             INTEGER NOT NULL DEFAULT 0,  -- 输入 Token 总量
    output_tokens            INTEGER NOT NULL DEFAULT 0,  -- 输出 Token 总量
    cached_input_tokens      INTEGER NOT NULL DEFAULT 0,  -- 缓存命中输入 Token 总量
    estimated_cost_micros   INTEGER,               -- 估算费用总额, 无价格快照时 NULL
    currency                TEXT                    -- 币种, 无费用估算时 NULL
);

-- 按日期查: 看板按天展示用量趋势
CREATE INDEX idx_usage_daily_date
    ON model_usage_daily(date DESC);

-- 按供应商+模型查: 看板按供应商和模型聚合
CREATE INDEX idx_usage_daily_provider_model
    ON model_usage_daily(provider, model, date DESC);

-- 按用户查: 看用户个人的用量趋势
CREATE INDEX idx_usage_daily_user
    ON model_usage_daily(user_id, date DESC);

-- 唯一索引: 防止同一日期+设备+用户+供应商+模型+操作的重复聚合
-- 聚合任务用 UPSERT 语义: 存在就累加, 不存在就插入
CREATE UNIQUE INDEX idx_usage_daily_unique
    ON model_usage_daily(date, COALESCE(device_id, ''), user_id, provider, model, operation);

-- 模型价格快照表: 记录每个供应商+模型的价格版本.
-- 方案 15.3 节: 价格会变化, 不能用今天的价格重新计算过去所有调用.
-- 每条 model_usages 记录保存调用时使用的价格版本和估算结果.
-- 字段含义:
--   input_price          — 输入 Token 单价(每百万 Token)
--   output_price         — 输出 Token 单价(每百万 Token)
--   cached_input_price   — 缓存命中输入 Token 单价(比正常输入便宜)
--   cache_creation_price — 创建缓存的单价(Anthropic 概念)
--   unit                 — 计价单位, 如 "per_million_tokens"
--   currency             — 币种, 如 "CNY", "USD"
--   source_version       — 价格来源版本标识, 方便追溯是哪次更新

CREATE TABLE model_price_snapshots (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    provider               TEXT NOT NULL,       -- 供应商名称, 如 "deepseek", "openai"
    model                  TEXT NOT NULL,       -- 模型名, 如 "deepseek-chat"
    effective_from         DATETIME NOT NULL,   -- 价格生效时间
    input_price            REAL,                -- 输入 Token 单价(每百万 Token)
    output_price           REAL,                -- 输出 Token 单价(每百万 Token)
    cached_input_price     REAL,                -- 缓存命中输入 Token 单价
    cache_creation_price   REAL,                -- 创建缓存的单价(Anthropic 概念)
    unit                   TEXT NOT NULL,       -- 计价单位, 如 "per_million_tokens"
    currency               TEXT NOT NULL,       -- 币种, 如 "CNY", "USD"
    source_version         TEXT,                -- 价格来源版本标识
    created_at             DATETIME NOT NULL    -- 记录创建时间
);

-- 按 provider + model + effective_from 查: 查某次调用时点的有效价格
CREATE INDEX idx_price_snapshots_provider_model_time
    ON model_price_snapshots(provider, model, effective_from DESC);

-- 按 provider + model 查: 查某个模型的最新价格
CREATE INDEX idx_price_snapshots_provider_model
    ON model_price_snapshots(provider, model, created_at DESC);

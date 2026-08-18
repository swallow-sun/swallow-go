-- 独立记录每次模型调用的 Token 用量和费用估算。
-- 之前 Token 用量存在 dialogues 表里，只跟对话消息绑在一起；
-- 阶段 2 要求独立建表，支持 chat/embedding/vision/asr/tts 多种操作类型，
-- 并且区分供应商返回了 0（明确没有消耗）和 NULL（没返回、不适用）。
-- 费用估算字段目前留空，等 model_price_snapshots 表建好后再填。

CREATE TABLE model_usages (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id               TEXT,                -- 本次模型调用的内部请求标识，目前用 trace_id
    trace_id                 TEXT NOT NULL,       -- 链路追踪 ID，关联 events 和 dialogues
    session_id               TEXT,                -- 哪个会话产生的调用
    user_id                  INTEGER,             -- 哪个用户产生的调用
    device_id                TEXT,                -- 哪个设备产生的调用（阶段 4+ 才有，目前 NULL）
    provider                 TEXT NOT NULL,       -- 供应商名称，如 "deepseek"、"openai"
    model                    TEXT NOT NULL,       -- 模型名，如 "deepseek-chat"
    operation                TEXT NOT NULL,       -- 操作类型：chat/embedding/vision/asr/tts
    input_tokens             INTEGER,            -- 输入 Token 总量
    output_tokens            INTEGER,            -- 输出 Token 数
    cached_input_tokens      INTEGER,            -- 输入中命中供应商缓存的部分
    cache_miss_tokens        INTEGER,            -- 缓存未命中的输入 Token（DeepSeek/OpenAI 返回 prompt_cache_miss_tokens）
    cache_creation_tokens    INTEGER,            -- 创建缓存时单独计量的输入（Anthropic 概念）；供应商不支持则 NULL
    reasoning_tokens         INTEGER,            -- 供应商明确返回的推理 Token；不支持则 NULL
    total_tokens             INTEGER,            -- 总 Token 数；按供应商返回值保存，缺失时才推导
    input_audio_seconds      REAL,               -- ASR 输入音频时长（秒），非 ASR 操作为 NULL
    output_audio_seconds     REAL,               -- TTS 输出音频时长（秒），非 TTS 操作为 NULL
    input_image_count        INTEGER,            -- 视觉操作输入图片数，非视觉操作为 NULL
    currency                 TEXT,                -- 费用币种，如 "USD"、"CNY"；无费用估算时 NULL
    estimated_cost_micros    INTEGER,            -- 估算费用，百万分之一货币单位；无估算时 NULL
    provider_request_id      TEXT,                -- 供应商返回的请求 ID（有些供应商不返回）
    status                   TEXT NOT NULL,       -- 调用状态：ok / failed
    duration_ms              INTEGER,            -- 调用耗时（毫秒）
    occurred_at              DATETIME NOT NULL    -- 调用发生时间
);

-- 按 trace_id 查：把同一次请求的 model_usages 和 events/dialogues 串起来
CREATE INDEX idx_model_usages_trace ON model_usages(trace_id);

-- 按 provider + model 查：看板按供应商和模型聚合用量
CREATE INDEX idx_model_usages_provider_model
    ON model_usages(provider, model, occurred_at DESC);

-- 按时间查：看板按天聚合
CREATE INDEX idx_model_usages_time ON model_usages(occurred_at DESC);

-- 按用户查：看用户个人的模型用量
CREATE INDEX idx_model_usages_user ON model_usages(user_id, occurred_at DESC);

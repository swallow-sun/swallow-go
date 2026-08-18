-- 链路追踪 Span 表：记录一次请求经过的每个处理步骤。
-- 方案 16.10.3：同一请求所有 Span 共享同一个 trace_id，
-- 通过 parent_span_id 组成父子树（Handler → ChatService → ModelProvider）。
-- 验收标准：duration_ms = 0 时仍能通过事件状态确认步骤已经执行。

CREATE TABLE spans (
    id               TEXT PRIMARY KEY,        -- Span ID（UUID），主键
    trace_id         TEXT NOT NULL,            -- 链路追踪 ID，关联 events/model_usages/dialogues
    parent_span_id   TEXT,                     -- 父 Span ID，根 Span 为 NULL；通过它组成调用树
    component        TEXT NOT NULL,            -- 组件名：handler / chat_service / model_provider
    operation        TEXT NOT NULL,            -- 操作名：POST /api/chat、stream_loop、llm.stream 等
    status           TEXT NOT NULL,            -- 状态：ok / error / cancelled
    duration_ms      INTEGER DEFAULT 0,        -- 耗时（毫秒）；0 表示步骤极快或未记录到
    started_at       DATETIME NOT NULL,        -- 开始时间
    finished_at      DATETIME,                 -- 结束时间；NULL 表示未结束（异常退出没来得及标记）
    attributes       TEXT                      -- 附加属性 JSON 字符串（如 model、error_code 等）
);

-- 按 trace_id 查：把同一次请求的所有 Span 串起来，组成调用链树
CREATE INDEX idx_spans_trace ON spans(trace_id, started_at ASC);

-- 按 parent_span_id 查：从某个 Span 往下找它的所有子 Span
CREATE INDEX idx_spans_parent ON spans(parent_span_id);

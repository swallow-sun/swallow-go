-- 为设备端记忆候选审核补充并发控制、稍后提醒和审核来源。
--
-- 本迁移只增加字段，不改变已有候选的状态，也不会自动确认或删除旧数据。
-- revision 从 1 开始；客户端提交决策时携带自己看到的 revision，服务端用
-- 条件 UPDATE 保证多设备并发时只有一个决策能够生效。

ALTER TABLE memory_candidates
    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;

ALTER TABLE memory_candidates
    ADD COLUMN deferred_until DATETIME;

ALTER TABLE memory_candidates
    ADD COLUMN resolved_by TEXT NOT NULL DEFAULT '';

ALTER TABLE memory_candidates
    ADD COLUMN resolved_device_id TEXT NOT NULL DEFAULT '';

-- 设备每次只拉取当前可展示的 pending 候选。deferred_until 为空表示立即可见。
CREATE INDEX idx_memory_candidates_review_queue
    ON memory_candidates(user_id, status, deferred_until, created_at);

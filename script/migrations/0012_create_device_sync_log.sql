-- 0012_create_device_sync_log.sql
-- 设备同步日志表: 记录设备上报的 sync_outbox 条目, 用 item_id 做幂等.
-- 设备重试时 ON CONFLICT DO NOTHING, 保证同一条记录不会重复入库.
CREATE TABLE IF NOT EXISTS device_sync_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    item_id TEXT NOT NULL,
    item_type TEXT NOT NULL,
    payload TEXT NOT NULL,
    received_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_device_sync_log_item
    ON device_sync_log(device_id, item_id);

-- 作用：修复 model_usage_daily 聚合唯一键在 NULL 用户或设备下无法去重的问题。
-- 原因：SQLite 认为 NULL 与 NULL 不冲突，旧唯一索引可能产生重复的系统级聚合行。
-- 处理：把历史 NULL 归一成明确零值，再改为普通列唯一索引，供 GORM ON CONFLICT 使用。
-- 边界：本迁移不建立外键，不删除任何用量统计数据。

-- 当前阶段空字符串表示“尚未关联设备”。
UPDATE model_usage_daily SET device_id = '' WHERE device_id IS NULL;

-- user_id=0 表示不属于具体用户的系统级模型调用。
UPDATE model_usage_daily SET user_id = 0 WHERE user_id IS NULL;

-- 删除旧的表达式唯一索引。
DROP INDEX idx_usage_daily_unique;

-- 新索引与 GORM 冲突列完全一致，保证同一聚合维度只能有一行。
CREATE UNIQUE INDEX idx_usage_daily_unique
    ON model_usage_daily(date, device_id, user_id, provider, model, operation);

-- 为每个用户维护全局单调递增的记忆同步版本。
-- 设备按 sync_version 拉取增量时，新增、编辑和删除共享同一版本空间。
CREATE TABLE IF NOT EXISTS memory_sync_cursors (
    user_id      INTEGER PRIMARY KEY,
    next_version INTEGER NOT NULL DEFAULT 1
);

-- 先给历史记忆分配版本，保证旧数据在设备首次同步时也能下发。
WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at, id) AS version
    FROM memories
    WHERE sync_version = 0
)
UPDATE memories
SET sync_version = (SELECT version FROM ranked WHERE ranked.id = memories.id)
WHERE id IN (SELECT id FROM ranked);

-- 墓碑版本接在该用户已有记忆版本之后，避免两类变更共用同一版本。
WITH ranked AS (
    SELECT t.id,
           COALESCE((SELECT MAX(m.sync_version) FROM memories m WHERE m.user_id = t.user_id), 0)
           + ROW_NUMBER() OVER (PARTITION BY t.user_id ORDER BY t.deleted_at, t.id) AS version
    FROM memory_tombstones t
    WHERE t.sync_version = 0
)
UPDATE memory_tombstones
SET sync_version = (SELECT version FROM ranked WHERE ranked.id = memory_tombstones.id)
WHERE id IN (SELECT id FROM ranked);

INSERT INTO memory_sync_cursors(user_id, next_version)
SELECT user_id, MAX(sync_version) + 1
FROM (
    SELECT user_id, sync_version FROM memories
    UNION ALL
    SELECT user_id, sync_version FROM memory_tombstones
)
GROUP BY user_id
ON CONFLICT(user_id) DO UPDATE SET next_version = MAX(next_version, excluded.next_version);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_user_sync_version
    ON memories(user_id, sync_version)
    WHERE sync_version > 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_tombstones_user_sync_version
    ON memory_tombstones(user_id, sync_version)
    WHERE sync_version > 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_versions_memory_version
    ON memory_versions(memory_id, version);

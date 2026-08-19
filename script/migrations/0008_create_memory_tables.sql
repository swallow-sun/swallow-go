-- 长期记忆四张表: 候选, 正式记忆, 版本历史, 删除标记.
-- 方案 16.11 节: 受控长期记忆.
-- 核心原则: 模型不能未经确认自动写入长期记忆.
--   1. 对话产生候选 → memory_candidates (pending)
--   2. 用户确认 → memory_candidates (confirmed) + memories (active)
--   3. 用户编辑 → memory_versions 记录每个版本
--   4. 用户删除 → memory_tombstones 标记删除, memories 软删为 deleted
--   5. 后续检索只查 memories 中 status=active 的记录

-- ============================================================
-- 1. memory_candidates: 记忆候选表
-- ============================================================
-- 方案 16.11.3 节: 用户说"我喜欢回答简短一点"后, 不应立即写入 memories,
-- 而是 memory_candidates 新增 pending, 等用户确认.
--
-- 字段含义:
--   id               — 自增主键
--   user_id          — 哪个用户的候选
--   session_id       — 来源会话 ID
--   trace_id         — 来源对话的 trace ID, 方便回溯是哪轮对话产生的
--   content          — 候选记忆内容, 比如"用户喜欢简短的回答"
--   memory_type      — 记忆类型: preference / fact / instruction / persona
--   source           — 来源: rule(规则产生) / model(模型建议)
--   reason           — 为什么建议保存, 给用户看的解释
--   usage_hint       — 保存后可能如何使用, 给用户看的解释
--   status           — 状态: pending / confirmed / rejected
--   created_at       — 创建时间
--   resolved_at      — 确认或拒绝的时间, NULL 表示还没处理

CREATE TABLE memory_candidates (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL,            -- 哪个用户的候选
    session_id       TEXT NOT NULL,               -- 来源会话 ID
    trace_id         TEXT,                         -- 来源对话的 trace ID
    content          TEXT NOT NULL,                -- 候选记忆内容
    memory_type      TEXT NOT NULL,                -- 记忆类型: preference/fact/instruction/persona
    source           TEXT NOT NULL DEFAULT 'rule', -- 来源: rule(规则) / model(模型建议)
    reason           TEXT NOT NULL DEFAULT '',     -- 为什么建议保存
    usage_hint       TEXT NOT NULL DEFAULT '',     -- 保存后可能如何使用
    status           TEXT NOT NULL DEFAULT 'pending', -- 状态: pending/confirmed/rejected
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at      DATETIME                       -- 确认或拒绝时间, NULL=未处理
);

-- 按用户+状态查: 用户查看自己的待确认候选列表
CREATE INDEX idx_memory_candidates_user_status
    ON memory_candidates(user_id, status, created_at DESC);

-- 按 trace_id 回溯: 查某轮对话产生了哪些候选
CREATE INDEX idx_memory_candidates_trace
    ON memory_candidates(trace_id);

-- ============================================================
-- 2. memories: 正式记忆表
-- ============================================================
-- 只有用户确认的候选才能写入 memories.
-- 检索时只查 status='active' 的记录, deleted 的不返回.
--
-- 字段含义:
--   id               — 自增主键
--   user_id          — 哪个用户的记忆
--   candidate_id     — 来源候选 ID, NULL 表示手动创建
--   source_session_id — 来源会话 ID
--   content          — 记忆内容
--   memory_type      — 记忆类型: preference / fact / instruction / persona
--   keywords         — 关键词, 空格分隔, 用于关键词检索(不用向量)
--   sync_version     — 同步版本号, 给 C++ 设备同步用(阶段 4+), 本阶段预留
--   status           — 状态: active / deleted
--   created_at       — 创建时间
--   updated_at       — 最后编辑时间

CREATE TABLE memories (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id           INTEGER NOT NULL,             -- 哪个用户的记忆
    candidate_id     INTEGER,                        -- 来源候选 ID, NULL=手动创建
    source_session_id TEXT,                          -- 来源会话 ID
    content          TEXT NOT NULL,                  -- 记忆内容
    memory_type      TEXT NOT NULL,                  -- 记忆类型: preference/fact/instruction/persona
    keywords         TEXT NOT NULL DEFAULT '',       -- 关键词, 空格分隔, 用于检索
    sync_version     INTEGER NOT NULL DEFAULT 0,     -- 同步版本号, C++ 设备同步用, 本阶段预留
    status           TEXT NOT NULL DEFAULT 'active', -- 状态: active/deleted
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 按用户查: 查看用户的所有正式记忆
CREATE INDEX idx_memories_user
    ON memories(user_id, status, updated_at DESC);

-- 按用户+类型查: 按记忆类型过滤
CREATE INDEX idx_memories_user_type
    ON memories(user_id, memory_type, status);

-- 按用户+关键词查: 关键词检索(第一版不用向量, 用 LIKE)
CREATE INDEX idx_memories_user_keywords
    ON memories(user_id, status);

-- ============================================================
-- 3. memory_versions: 记忆编辑历史表
-- ============================================================
-- 用户编辑记忆内容时, 旧版本存到这里, 方便回溯修改历史.
--
-- 字段含义:
--   id          — 自增主键
--   memory_id   — 哪条记忆的版本
--   version     — 版本号, 从 1 开始递增
--   content     — 该版本的内容
--   keywords    — 该版本的关键词
--   edited_by   — 编辑者: user / system
--   created_at  — 该版本的创建时间

CREATE TABLE memory_versions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id   INTEGER NOT NULL,               -- 哪条记忆的版本
    version     INTEGER NOT NULL,               -- 版本号, 从 1 开始
    content     TEXT NOT NULL,                  -- 该版本的内容
    keywords    TEXT NOT NULL DEFAULT '',        -- 该版本的关键词
    edited_by   TEXT NOT NULL DEFAULT 'user',   -- 编辑者: user/system
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
);

-- 按记忆查版本历史
CREATE INDEX idx_memory_versions_memory
    ON memory_versions(memory_id, version DESC);

-- ============================================================
-- 4. memory_tombstones: 删除标记表
-- ============================================================
-- 方案 16.11.4 节: "删除记忆后普通查询和缓存都不再返回它".
-- 用户删除记忆时, memories 表软删(status=deleted), 同时写一条 tombstone.
-- tombstone 的作用: 防止已删除的记忆通过同步机制重新出现.
--
-- 字段含义:
--   id          — 自增主键
--   memory_id   — 被删除的记忆 ID
--   user_id     — 哪个用户删的
--   sync_version — 删除时的同步版本号, 给 C++ 设备同步用
--   deleted_at  — 删除时间

CREATE TABLE memory_tombstones (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id    INTEGER NOT NULL,              -- 被删除的记忆 ID
    user_id      INTEGER NOT NULL,              -- 哪个用户删的
    sync_version INTEGER NOT NULL DEFAULT 0,     -- 删除时的同步版本号
    deleted_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
);

-- 按用户查 tombstone: 同步时检查哪些记忆已被删除
CREATE INDEX idx_memory_tombstones_user
    ON memory_tombstones(user_id, deleted_at DESC);

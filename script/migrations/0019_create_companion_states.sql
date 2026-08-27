-- 关系人格状态：用于让 Agent 的关心、着急、调侃和亲昵表达跨会话连续。
-- 这是可配置的行为状态，不代表机器具有真实主观意识。
CREATE TABLE companion_states (
    user_id                 INTEGER PRIMARY KEY,
    concern                 REAL NOT NULL DEFAULT 0,
    urgency                 REAL NOT NULL DEFAULT 0,
    fondness                REAL NOT NULL DEFAULT 0.5,
    playfulness             REAL NOT NULL DEFAULT 0.3,
    allow_teasing           INTEGER NOT NULL DEFAULT 1,
    allow_strict_reminder   INTEGER NOT NULL DEFAULT 1,
    allow_affection         INTEGER NOT NULL DEFAULT 1,
    last_mode               TEXT NOT NULL DEFAULT 'neutral',
    current_task            TEXT NOT NULL DEFAULT '',
    task_updated_at         DATETIME,
    interaction_count       INTEGER NOT NULL DEFAULT 0,
    updated_at              DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 作用：把可动态调整的模型配置和敏感凭据迁移到 SQLite。
-- 安全：普通配置允许明文；API Key 和 owner token 只允许保存 AES-256-GCM 密文。
-- 边界：主加密密钥 SWALLOW_MASTER_KEY 永远不进入本表、配置文件或 Git。
-- 旧数据：本迁移只新增表，不修改 users、sessions、dialogues 等已有数据。
-- 恢复：迁移在事务中执行，失败时两张新表和索引全部回滚。

-- app_settings：保存不敏感、可以直接读取的运行配置。
CREATE TABLE app_settings (
    setting_key TEXT PRIMARY KEY,   -- 稳定配置键，例如 llm.model、llm.base_url
    setting_value TEXT NOT NULL,    -- 配置值；本表禁止保存密钥和令牌
    value_type TEXT NOT NULL,       -- 值类型；当前支持 string
    description TEXT,               -- 配置用途的中文说明
    updated_at DATETIME NOT NULL    -- 配置最后更新时间
);

-- encrypted_secrets：保存必须可还原使用、但不能明文落盘的敏感配置。
CREATE TABLE encrypted_secrets (
    secret_key TEXT PRIMARY KEY,    -- 稳定密钥名，例如 llm.api_key、auth.owner_token
    ciphertext BLOB NOT NULL,       -- AES-GCM 产生的密文，包含认证标签
    nonce BLOB NOT NULL,            -- 每次加密随机生成的 nonce，同一主密钥下不得复用
    algorithm TEXT NOT NULL,        -- 加密算法；当前固定 aes-256-gcm
    key_version INTEGER NOT NULL,   -- 主密钥版本；为后续轮换预留
    updated_at DATETIME NOT NULL    -- 密文最后更新时间
);

-- 支持按更新时间检查最近变更的普通配置。
CREATE INDEX idx_app_settings_updated ON app_settings(updated_at DESC);

-- 支持密钥轮换时按版本筛选需要重新加密的记录。
CREATE INDEX idx_encrypted_secrets_key_version
    ON encrypted_secrets(key_version, updated_at DESC);

-- devices 表保存已经注册到 Go 服务端的嵌入式设备身份.
-- 设备注册时生成高强度随机令牌,明文只返回一次;数据库只保存 SHA-256 摘要.
-- 本表不使用数据库外键,用户归属和状态校验由 Repository 与 Service 负责.
CREATE TABLE IF NOT EXISTS devices (
    -- id 是服务端生成的设备 UUID,也是设备后续请求携带的公开标识.
    id TEXT PRIMARY KEY,
    -- user_id 表示设备属于哪个用户,当前第一版固定为 owner 用户.
    user_id INTEGER NOT NULL,
    -- name 是用户可读的设备名称,例如 bench-a 或 swallow-01.
    name TEXT NOT NULL,
    -- platform 表示设备运行平台,例如 linux-arm64 或 windows-amd64.
    platform TEXT NOT NULL DEFAULT '',
    -- token_hash 是设备认证令牌的 SHA-256 十六进制摘要,不保存令牌明文.
    token_hash TEXT NOT NULL,
    -- status 是设备状态,active 表示允许认证,revoked 表示已经吊销.
    status TEXT NOT NULL DEFAULT 'active',
    -- capabilities_json 保存设备上报的能力 JSON,第一版允许为空对象.
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    -- created_at 是设备首次注册时间.
    created_at DATETIME NOT NULL,
    -- last_seen_at 是最近一次认证成功时间,注册后尚未请求时允许为空.
    last_seen_at DATETIME,
    -- revoked_at 是设备被吊销的时间,正常设备为空.
    revoked_at DATETIME
);

-- 同一用户下设备名称唯一,避免重复注册产生难以区分的设备记录.
CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_user_name
ON devices(user_id, name);

-- 设备认证和管理列表经常按用户与状态查询.
CREATE INDEX IF NOT EXISTS idx_devices_user_status
ON devices(user_id, status, created_at DESC);

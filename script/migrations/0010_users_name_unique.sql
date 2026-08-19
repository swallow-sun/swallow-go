-- Add unique index on users.name to prevent duplicate user creation under concurrency.
-- LoginOrCreateUser does a SELECT-then-INSERT; without this index, two concurrent
-- requests with the same name would both succeed, creating duplicate rows.

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_name ON users(name);

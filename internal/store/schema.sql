CREATE TABLE IF NOT EXISTS schema_migrations (
    version integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS employees (
    id text PRIMARY KEY,
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    payload jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS records (
    id text PRIMARY KEY,
    kind text NOT NULL,
    employee_id text NOT NULL REFERENCES employees(id),
    payload jsonb NOT NULL
);

CREATE INDEX IF NOT EXISTS records_kind_employee ON records(kind, employee_id);

CREATE TABLE IF NOT EXISTS files (
    id text PRIMARY KEY,
    employee_id text NOT NULL REFERENCES employees(id),
    payload jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    hash text PRIMARY KEY,
    employee_id text NOT NULL REFERENCES employees(id),
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS audit_log (
    id text PRIMARY KEY,
    payload jsonb NOT NULL
);

INSERT INTO schema_migrations(version) VALUES (1) ON CONFLICT DO NOTHING;

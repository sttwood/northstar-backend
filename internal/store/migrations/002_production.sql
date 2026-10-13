CREATE TABLE IF NOT EXISTS companies (
    id text PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    payload jsonb NOT NULL
);
INSERT INTO companies(id, slug, payload)
VALUES ('default', 'northstar', '{"id":"default","name":"Northstar","slug":"northstar","timezone":"Asia/Bangkok","workdayHours":8,"workdayStart":"09:00","workdayEnd":"18:00","requireMfa":false,"approvalStages":["hr"],"retentionDays":3650,"auditRetentionDays":3650,"notificationRetentionDays":90,"country":"TH","taxYear":2026}')
ON CONFLICT DO NOTHING;

ALTER TABLE employees ADD COLUMN IF NOT EXISTS company_id text NOT NULL DEFAULT 'default' REFERENCES companies(id);
ALTER TABLE employees DROP CONSTRAINT IF EXISTS employees_email_key;
CREATE UNIQUE INDEX IF NOT EXISTS employees_company_email ON employees(company_id, email);
ALTER TABLE records ADD COLUMN IF NOT EXISTS company_id text NOT NULL DEFAULT 'default' REFERENCES companies(id);
ALTER TABLE files ADD COLUMN IF NOT EXISTS company_id text NOT NULL DEFAULT 'default' REFERENCES companies(id);
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS company_id text NOT NULL DEFAULT 'default' REFERENCES companies(id);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS assurance text NOT NULL DEFAULT 'password';

CREATE TABLE IF NOT EXISTS operations (
    id text PRIMARY KEY,
    company_id text NOT NULL REFERENCES companies(id),
    kind text NOT NULL,
    employee_id text,
    status text NOT NULL,
    payload jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS account_security (
    employee_id text PRIMARY KEY REFERENCES employees(id),
    company_id text NOT NULL REFERENCES companies(id),
    payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS records_company_kind_id ON records(company_id, kind, id);
CREATE INDEX IF NOT EXISTS records_company_employee_id ON records(company_id, employee_id, id);
CREATE INDEX IF NOT EXISTS records_company_status ON records(company_id, (payload->>'status'), id);
CREATE INDEX IF NOT EXISTS records_company_project ON records(company_id, (payload->'data'->>'projectId'), id);
CREATE INDEX IF NOT EXISTS files_company_employee_id ON files(company_id, employee_id, id);
CREATE INDEX IF NOT EXISTS audit_company_id ON audit_log(company_id, id);
CREATE INDEX IF NOT EXISTS operations_company_kind_status ON operations(company_id, kind, status, id);
CREATE UNIQUE INDEX IF NOT EXISTS companies_drive_folder_unique ON companies((payload->>'driveFolderId')) WHERE COALESCE(payload->>'driveFolderId','') <> '';
CREATE UNIQUE INDEX IF NOT EXISTS account_security_google_subject ON account_security(company_id, (payload->>'googleSub')) WHERE COALESCE(payload->>'googleSub','') <> '';
INSERT INTO schema_migrations(version) VALUES (2) ON CONFLICT DO NOTHING;

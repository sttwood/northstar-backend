CREATE INDEX IF NOT EXISTS points_adjustment_employee ON operations(company_id, employee_id, id) WHERE kind = 'points-adjustment';
INSERT INTO schema_migrations(version) VALUES (6) ON CONFLICT DO NOTHING;

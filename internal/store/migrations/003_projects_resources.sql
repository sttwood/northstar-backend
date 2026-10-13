CREATE INDEX IF NOT EXISTS records_company_parent_project
    ON records(company_id, (payload->'data'->>'parentId'), id)
    WHERE kind='project';
CREATE INDEX IF NOT EXISTS files_company_project_resource
    ON files(company_id, (payload->>'projectId'), id)
    WHERE payload->>'category'='project-resource';
CREATE INDEX IF NOT EXISTS files_company_reference_template
    ON files(company_id, (payload->>'recordId'), id)
    WHERE payload->>'category'='payslip-reference';
INSERT INTO schema_migrations(version) VALUES (3) ON CONFLICT DO NOTHING;

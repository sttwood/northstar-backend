CREATE INDEX IF NOT EXISTS community_posts_page_idx ON records(company_id,(payload->>'createdAt') DESC,id DESC) WHERE kind='community-post';
CREATE INDEX IF NOT EXISTS community_answers_page_idx ON records(company_id,(payload->'data'->>'postId'),(payload->>'createdAt') DESC,id DESC) WHERE kind='community-answer';
CREATE INDEX IF NOT EXISTS community_authors_idx ON records(company_id,employee_id,kind) WHERE kind IN ('community-post','community-answer');
CREATE INDEX IF NOT EXISTS community_activity_idx ON operations(company_id,employee_id,(payload->'data'->>'date')) WHERE kind='community-activity';
INSERT INTO schema_migrations(version) VALUES (4) ON CONFLICT DO NOTHING;

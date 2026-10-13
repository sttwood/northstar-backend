CREATE INDEX IF NOT EXISTS shop_catalog_page ON records(company_id, id) WHERE kind = 'shop-item';
CREATE INDEX IF NOT EXISTS shop_purchase_employee ON operations(company_id, employee_id, id) WHERE kind = 'shop-purchase';
CREATE INDEX IF NOT EXISTS shop_fulfillment_page ON operations(company_id, id) WHERE kind = 'shop-purchase' AND payload->>'status' = 'pending';
CREATE INDEX IF NOT EXISTS notification_page ON operations(company_id, employee_id, (payload->>'createdAt') DESC, id DESC) WHERE kind = 'notification';
CREATE INDEX IF NOT EXISTS notification_unread ON operations(company_id, employee_id) WHERE kind = 'notification' AND payload->>'status' = 'unread';
INSERT INTO schema_migrations(version) VALUES (5) ON CONFLICT DO NOTHING;

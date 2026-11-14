-- Add connector provenance without rebuilding the v3 identity graph. Keeping
-- every existing table in place preserves AI composite foreign keys, message
-- sequences, local management flags and the external-content FTS index.
-- The original source columns remain the v1-v3 compatibility representation;
-- Store reads coalesce(connector_source, source) as the effective source.
ALTER TABLE connections ADD COLUMN connector_source TEXT
 CHECK(connector_source IS NULL OR (platform='weixin' AND connector_source IN ('wechatdb','mixed')));
ALTER TABLE messages ADD COLUMN connector_source TEXT
 CHECK(connector_source IS NULL OR connector_source='wechatdb');

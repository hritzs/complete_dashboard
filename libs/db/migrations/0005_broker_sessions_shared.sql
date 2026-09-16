-- Adds what's needed for libs/broker-greeksoft's LoginShared to use
-- broker_sessions as a cross-process shared session cache: reconciler,
-- greeksoft-feed-bridge, and execution-gateway each independently calling
-- PerformFullLogin for the same GreekSoft account was invalidating each
-- other's session (GreekSoft appears to allow only one valid session per
-- account) -- confirmed live: a reconciler's Iris connection was kicked
-- with a graceful "close 1000 (normal)" immediately after another process
-- logged in separately. Sharing one login across processes closes this gap.
--
-- broker_sessions.session_token already holds the auth token; this adds
-- storage for the broker-specific fields (gcid, session_id, iris/apollo
-- endpoints, heartbeat interval) that PerformFullLogin also produces and
-- that Iris/Apollo websocket connections need.
ALTER TABLE broker_sessions
    ADD COLUMN IF NOT EXISTS broker_specific JSONB;

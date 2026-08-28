CREATE TABLE IF NOT EXISTS charges (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    amount BIGINT NOT NULL,
    currency CHAR(3) NOT NULL,
    payment_id TEXT NOT NULL UNIQUE,
    callback_url TEXT NOT NULL,
    qr_code TEXT NOT NULL,
    copy_paste TEXT NOT NULL,
    provider_tx_id TEXT NOT NULL,
    event_id TEXT,
    event_type TEXT,
    last_delivery_status TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id BIGSERIAL PRIMARY KEY,
    charge_id TEXT NOT NULL REFERENCES charges (id),
    event_id TEXT NOT NULL UNIQUE,
    callback_url TEXT NOT NULL,
    payload BYTEA NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_status TEXT,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS outbox_events_pending_idx
    ON outbox_events (next_attempt_at)
    WHERE delivered_at IS NULL;

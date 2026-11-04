-- +goose Up
CREATE TABLE idempotency_keys (
    key          UUID PRIMARY KEY,
    request_hash TEXT NOT NULL,
    -- DEFERRABLE INITIALLY DEFERRED ключ вставляется раньше поездки, поэтому проверка откладывается до COMMIT
    trip_id      UUID NOT NULL REFERENCES trips(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Для будущей очистки просроченных ключей
CREATE INDEX idempotency_keys_created_at_idx ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;

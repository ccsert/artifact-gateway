-- +goose Up
ALTER TABLE native_raw_uploads
    ADD COLUMN IF NOT EXISTS collected_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS native_raw_uploads_reclaim_idx
    ON native_raw_uploads (expires_at, id)
    WHERE state = 'open' OR (state IN ('completed', 'cancelled') AND collected_at IS NULL);

-- +goose Down
-- Raw upload cleanup state is additive; compensate forward rather than removing it.

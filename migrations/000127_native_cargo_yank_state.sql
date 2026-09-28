-- +goose Up
-- The source archive and publication metadata stay immutable. Only the
-- sparse-index yank flag may change after publication.
ALTER TABLE native_cargo_publications
    ADD COLUMN yanked BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN description TEXT NOT NULL DEFAULT '';
UPDATE native_cargo_publications SET updated_at=created_at;

-- +goose Down
-- Cargo publication state is forward-only; compensate with a later migration.

-- +goose Up
ALTER TABLE native_conan_object_intents
    ADD COLUMN collecting_at TIMESTAMPTZ;

-- The delete boundary survives object-store errors and worker restarts. All
-- new references reject collecting objects before making metadata visible.

-- +goose Down
-- Forward-only: removing the durable fence could expose bytes already deleted.

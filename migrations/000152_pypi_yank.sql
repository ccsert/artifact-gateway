-- +goose Up
ALTER TABLE native_pypi_files
  ADD COLUMN yanked boolean NOT NULL DEFAULT false,
  ADD COLUMN yank_reason text NOT NULL DEFAULT '',
  ADD COLUMN yank_revision bigint NOT NULL DEFAULT 1 CHECK (yank_revision > 0),
  ADD CONSTRAINT native_pypi_yank_reason_size CHECK (octet_length(yank_reason) <= 1024),
  ADD CONSTRAINT native_pypi_unyanked_reason CHECK (yanked OR yank_reason = '');

-- +goose Down
-- Forward-only: removing advisory selection state would expose yanked files.

-- +goose Up
ALTER TABLE repository_security_policies
ADD COLUMN auto_scan_on_publish BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE repository_security_policies
DROP COLUMN auto_scan_on_publish;

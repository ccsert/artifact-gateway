-- +goose Up
ALTER TABLE oci_group_members
    ADD COLUMN IF NOT EXISTS repository_id UUID REFERENCES hosted_repositories(id);

ALTER TABLE maven_group_members
    ADD COLUMN IF NOT EXISTS repository_id UUID REFERENCES hosted_repositories(id);

ALTER TABLE raw_group_members
    ADD COLUMN IF NOT EXISTS repository_id UUID REFERENCES hosted_repositories(id);

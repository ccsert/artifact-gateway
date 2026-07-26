-- +goose Up
CREATE INDEX IF NOT EXISTS native_conan_recipe_revisions_visible_reference_idx
    ON native_conan_recipe_revisions (repository_id, reference)
    WHERE state = 'visible';

-- +goose Down
-- Keep this index as an additive performance projection.

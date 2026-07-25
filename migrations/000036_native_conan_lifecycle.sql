-- +goose Up
CREATE TABLE native_conan_object_intents (
    object_key TEXT PRIMARY KEY,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    size BIGINT NOT NULL CHECK (size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at TIMESTAMPTZ
);

CREATE TABLE native_conan_recipe_revisions (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    reference TEXT NOT NULL,
    revision TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    state TEXT NOT NULL CHECK (state IN ('visible', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, reference, revision)
);

CREATE TABLE native_conan_package_revisions (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    reference TEXT NOT NULL,
    recipe_revision TEXT NOT NULL,
    package_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    state TEXT NOT NULL CHECK (state IN ('visible', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, reference, recipe_revision, package_id, revision),
    FOREIGN KEY (repository_id, reference, recipe_revision)
        REFERENCES native_conan_recipe_revisions(repository_id, reference, revision)
);

CREATE TABLE native_conan_assets (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    reference TEXT NOT NULL,
    recipe_revision TEXT NOT NULL,
    package_id TEXT NOT NULL DEFAULT '',
    package_revision TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    object_key TEXT NOT NULL REFERENCES native_conan_object_intents(object_key),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    size BIGINT NOT NULL CHECK (size >= 0),
    PRIMARY KEY (repository_id, reference, recipe_revision, package_id, package_revision, path)
);

CREATE INDEX native_conan_assets_object_key_idx ON native_conan_assets (object_key);

-- +goose Up
ALTER TABLE hosted_repositories
    ADD COLUMN IF NOT EXISTS maven_strict_publication BOOLEAN NOT NULL DEFAULT false;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'hosted_repositories_maven_strict_publication_check') THEN
        ALTER TABLE hosted_repositories
            ADD CONSTRAINT hosted_repositories_maven_strict_publication_check
            CHECK (NOT maven_strict_publication OR (format = 'maven' AND repo_type = 'hosted'));
    END IF;
END $$;

-- +goose Down
-- Repository policy schema is additive; compensate forward rather than
-- removing a column that an older application can safely ignore.

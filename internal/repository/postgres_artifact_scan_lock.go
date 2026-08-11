package repository

import "context"

func (s *PostgresStore) LockArtifactScanIdentity(ctx context.Context, repositoryID string, format Format, coordinate, digest string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, artifactScanLockKey(repositoryID, format, coordinate, digest)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, artifactScanLockKey(repositoryID, format, coordinate, digest))
		_ = conn.Close()
	}, nil
}

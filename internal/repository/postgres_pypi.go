package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"time"
)

func (s *PostgresStore) LockPyPIObject(ctx context.Context, objectKey string) (func(), error) {
	_, release, err := s.LockArtifactObjectKeys(ctx, FormatPyPI, []string{objectKey})
	return release, err
}

type pypiRowScanner interface {
	Scan(...any) error
}

func scanPyPIFile(row pypiRowScanner, file *PyPIFile) error {
	var cachedAt, deletedAt, collectedAt sql.NullTime
	err := row.Scan(&file.RepositoryID, &file.Project, &file.Version, &file.Filename, &file.FileType,
		&file.PythonVersion, &file.RequiresPython, &file.Digest, &file.ObjectKey, &file.Size,
		&file.Publisher, &file.SourceURL, &file.State, &cachedAt, &deletedAt, &collectedAt, &file.CreatedAt)
	if cachedAt.Valid {
		file.CachedAt = cachedAt.Time
	}
	if deletedAt.Valid {
		file.DeletedAt = deletedAt.Time
	}
	if collectedAt.Valid {
		file.CollectedAt = collectedAt.Time
	}
	return err
}

const pypiFileColumns = `repository_id::text,project,version,filename,file_type,python_version,
requires_python,digest,object_key,size,publisher,source_url,state,cached_at,deleted_at,collected_at,created_at`

func pypiNullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (s *PostgresStore) PublishPyPIFile(ctx context.Context, file PyPIFile) (PyPIFile, error) {
	releaseDistribution, err := lockArtifactDistributionCoordinates(ctx, s, file.RepositoryID, FormatPyPI, []string{file.Project + "@" + file.Version})
	if err != nil {
		return PyPIFile{}, err
	}
	defer releaseDistribution()
	if file.CreatedAt.IsZero() {
		file.CreatedAt = time.Now().UTC()
	}
	if file.ObjectKey != "" && file.CachedAt.IsZero() {
		file.CachedAt = time.Now().UTC()
	}
	if file.State == "" {
		file.State = "visible"
	}
	var stored PyPIFile
	err = scanPyPIFile(s.db.QueryRowContext(ctx, `
		INSERT INTO native_pypi_files
		(repository_id,project,version,filename,file_type,python_version,requires_python,digest,object_key,size,publisher,source_url,state,cached_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (repository_id,filename) DO NOTHING
		RETURNING `+pypiFileColumns,
		file.RepositoryID, file.Project, file.Version, file.Filename, file.FileType, file.PythonVersion,
		file.RequiresPython, file.Digest, file.ObjectKey, file.Size, file.Publisher, file.SourceURL,
		file.State, pypiNullableTime(file.CachedAt), file.CreatedAt), &stored)
	if errors.Is(err, sql.ErrNoRows) {
		existing, lookupErr := s.getPyPIFileAnyState(ctx, file.RepositoryID, file.Filename)
		if lookupErr == nil && existing.Digest == file.Digest && existing.Project == file.Project && existing.Version == file.Version {
			return existing, nil
		}
		if lookupErr == nil {
			return PyPIFile{}, ErrNameExists
		}
		return PyPIFile{}, lookupErr
	}
	return stored, err
}

func (s *PostgresStore) PublishPyPIVersion(ctx context.Context, files []PyPIFile) ([]PyPIFile, error) {
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	releaseDistribution, err := lockArtifactDistributionCoordinates(ctx, s, files[0].RepositoryID, FormatPyPI, []string{files[0].Project + "@" + files[0].Version})
	if err != nil {
		return nil, err
	}
	defer releaseDistribution()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	result := make([]PyPIFile, 0, len(files))
	for _, file := range files {
		if file.RepositoryID != files[0].RepositoryID || file.Project != files[0].Project || file.Version != files[0].Version || file.ObjectKey == "" {
			return nil, ErrDisabled
		}
		var existing PyPIFile
		lookupErr := scanPyPIFile(tx.QueryRowContext(ctx, `SELECT `+pypiFileColumns+` FROM native_pypi_files WHERE repository_id::text=$1 AND filename=$2 FOR UPDATE`, file.RepositoryID, file.Filename), &existing)
		if lookupErr == nil {
			if existing.State != "visible" || existing.Digest != file.Digest || existing.Project != file.Project || existing.Version != file.Version {
				return nil, ErrNameExists
			}
			result = append(result, existing)
			continue
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return nil, lookupErr
		}
		if file.CreatedAt.IsZero() {
			file.CreatedAt = now
		}
		if file.CachedAt.IsZero() {
			file.CachedAt = now
		}
		file.State = "visible"
		var stored PyPIFile
		err = scanPyPIFile(tx.QueryRowContext(ctx, `INSERT INTO native_pypi_files
			(repository_id,project,version,filename,file_type,python_version,requires_python,digest,object_key,size,publisher,source_url,state,cached_at,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'visible',$13,$14) RETURNING `+pypiFileColumns,
			file.RepositoryID, file.Project, file.Version, file.Filename, file.FileType, file.PythonVersion,
			file.RequiresPython, file.Digest, file.ObjectKey, file.Size, file.Publisher, file.SourceURL, file.CachedAt, file.CreatedAt), &stored)
		if err != nil {
			return nil, err
		}
		result = append(result, stored)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *PostgresStore) SyncPyPIProxyFiles(ctx context.Context, repositoryID, project string, files []PyPIFile) error {
	coordinates := make([]string, 0, len(files))
	for _, file := range files {
		coordinates = append(coordinates, project+"@"+file.Version)
	}
	releaseDistribution, err := lockArtifactDistributionCoordinates(ctx, s, repositoryID, FormatPyPI, coordinates)
	if err != nil {
		return err
	}
	defer releaseDistribution()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, file := range files {
		var digest string
		err = tx.QueryRowContext(ctx, `SELECT digest FROM native_pypi_files WHERE repository_id::text=$1 AND filename=$2 FOR UPDATE`, repositoryID, file.Filename).Scan(&digest)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `
				INSERT INTO native_pypi_files
				(repository_id,project,version,filename,file_type,python_version,requires_python,digest,publisher,source_url,state)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'visible')`, repositoryID, project, file.Version,
				file.Filename, file.FileType, file.PythonVersion, file.RequiresPython, file.Digest, file.Publisher, file.SourceURL)
		case err != nil:
			return err
		case digest != file.Digest:
			return ErrUpstreamChanged
		default:
			_, err = tx.ExecContext(ctx, `
				UPDATE native_pypi_files SET requires_python=$3,publisher=$4,source_url=$5
				WHERE repository_id::text=$1 AND filename=$2`, repositoryID, file.Filename, file.RequiresPython, file.Publisher, file.SourceURL)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) CachePyPIProxyFile(ctx context.Context, incoming PyPIFile) (PyPIFile, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PyPIFile{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var stored PyPIFile
	err = scanPyPIFile(tx.QueryRowContext(ctx, `SELECT `+pypiFileColumns+` FROM native_pypi_files
		WHERE repository_id::text=$1 AND filename=$2 AND state='visible' FOR UPDATE`, incoming.RepositoryID, incoming.Filename), &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return PyPIFile{}, ErrNotFound
	}
	if err != nil {
		return PyPIFile{}, err
	}
	if stored.Digest != incoming.Digest || stored.SourceURL != incoming.SourceURL {
		return PyPIFile{}, ErrUpstreamChanged
	}
	if stored.ObjectKey != "" {
		return stored, tx.Commit()
	}
	err = scanPyPIFile(tx.QueryRowContext(ctx, `UPDATE native_pypi_files
		SET object_key=$3,size=$4,cached_at=$5
		WHERE repository_id::text=$1 AND filename=$2
		RETURNING `+pypiFileColumns, incoming.RepositoryID, incoming.Filename, incoming.ObjectKey, incoming.Size, incoming.CachedAt), &stored)
	if err != nil {
		return PyPIFile{}, err
	}
	if err = tx.Commit(); err != nil {
		return PyPIFile{}, err
	}
	return stored, nil
}

func (s *PostgresStore) getPyPIFileAnyState(ctx context.Context, repositoryID, filename string) (PyPIFile, error) {
	var file PyPIFile
	err := scanPyPIFile(s.db.QueryRowContext(ctx, `SELECT `+pypiFileColumns+` FROM native_pypi_files WHERE repository_id::text=$1 AND filename=$2`, repositoryID, filename), &file)
	if errors.Is(err, sql.ErrNoRows) {
		return PyPIFile{}, ErrNotFound
	}
	return file, err
}

func (s *PostgresStore) GetPyPIFile(ctx context.Context, repositoryID, filename string) (PyPIFile, error) {
	file, err := s.getPyPIFileAnyState(ctx, repositoryID, filename)
	if err == nil && file.State != "visible" {
		return PyPIFile{}, ErrNotFound
	}
	return file, err
}

func (s *PostgresStore) ListPyPIProjectFiles(ctx context.Context, repositoryID, project string) ([]PyPIFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pypiFileColumns+` FROM native_pypi_files
		WHERE repository_id::text=$1 AND project=$2 AND state='visible'
		ORDER BY created_at DESC,version DESC,filename`, repositoryID, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	files := make([]PyPIFile, 0)
	for rows.Next() {
		var file PyPIFile
		if err = scanPyPIFile(rows, &file); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	return files, nil
}

func (s *PostgresStore) ListPyPIProjects(ctx context.Context, repositoryID, prefix string, limit int, after string) ([]PyPIProjectSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT project,count(*),count(DISTINCT version),min(created_at),max(created_at),
		       (array_agg(version ORDER BY created_at DESC,filename))[1],
		       (array_agg(filename ORDER BY created_at DESC,filename))[1],
		       (array_agg(file_type ORDER BY created_at DESC,filename))[1],
		       (array_agg(python_version ORDER BY created_at DESC,filename))[1],
		       (array_agg(requires_python ORDER BY created_at DESC,filename))[1],
		       (array_agg(digest ORDER BY created_at DESC,filename))[1],
		       (array_agg(object_key ORDER BY created_at DESC,filename))[1],
		       (array_agg(size ORDER BY created_at DESC,filename))[1],
		       (array_agg(publisher ORDER BY created_at DESC,filename))[1],
		       (array_agg(source_url ORDER BY created_at DESC,filename))[1],
		       (array_agg(state ORDER BY created_at DESC,filename))[1],
		       (array_agg(cached_at ORDER BY created_at DESC,filename))[1],
		       (array_agg(created_at ORDER BY created_at DESC,filename))[1]
		FROM native_pypi_files
		WHERE repository_id::text=$1 AND project LIKE $2 || '%' AND project>$3 AND state='visible'
		GROUP BY project ORDER BY project LIMIT $4`, repositoryID, prefix, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]PyPIProjectSummary, 0, limit)
	for rows.Next() {
		var summary PyPIProjectSummary
		var cachedAt sql.NullTime
		file := PyPIFile{RepositoryID: repositoryID, State: "visible"}
		if err = rows.Scan(&summary.Project, &summary.FileCount, &summary.VersionCount, &summary.CreatedAt, &summary.UpdatedAt,
			&file.Version, &file.Filename, &file.FileType, &file.PythonVersion, &file.RequiresPython,
			&file.Digest, &file.ObjectKey, &file.Size, &file.Publisher, &file.SourceURL, &file.State, &cachedAt, &file.CreatedAt); err != nil {
			return nil, err
		}
		file.Project = summary.Project
		if cachedAt.Valid {
			file.CachedAt = cachedAt.Time
		}
		summary.RepositoryID = repositoryID
		summary.Latest = file
		result = append(result, summary)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *PostgresStore) TombstonePyPIVersion(ctx context.Context, repositoryID, project, version string) ([]PyPIFile, error) {
	releaseDistribution, err := lockArtifactDistributionCoordinates(ctx, s, repositoryID, FormatPyPI, []string{project + "@" + version})
	if err != nil {
		return nil, err
	}
	defer releaseDistribution()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `UPDATE native_pypi_files SET state='deleted',deleted_at=now()
		WHERE repository_id::text=$1 AND project=$2 AND version=$3 AND state='visible'
		RETURNING `+pypiFileColumns, repositoryID, project, version)
	if err != nil {
		return nil, err
	}
	files := make([]PyPIFile, 0)
	for rows.Next() {
		var file PyPIFile
		if err = scanPyPIFile(rows, &file); err != nil {
			_ = rows.Close()
			return nil, err
		}
		files = append(files, file)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate)
		VALUES ($1,'pypi',$2) ON CONFLICT (repository_id,format,coordinate)
		DO UPDATE SET tombstoned_at=now()`, repositoryID, project+"@"+version); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return files, nil
}

func (s *PostgresStore) RestorePyPIVersion(ctx context.Context, repositoryID, project, version string) ([]PyPIFile, error) {
	for attempt := 0; attempt < 3; attempt++ {
		objectKeys, found, collected, err := s.deletedPyPIObjectKeys(ctx, repositoryID, project, version)
		if err != nil {
			return nil, err
		}
		if !found || collected {
			return nil, ErrDisabled
		}
		objectCtx, releaseObjects, err := LockObjectKeys(ctx, objectKeys, s, FormatPyPI, s.LockPyPIObject)
		if err != nil {
			return nil, err
		}
		releaseDistribution, err := lockArtifactDistributionCoordinates(objectCtx, s, repositoryID, FormatPyPI, []string{project + "@" + version})
		if err != nil {
			releaseObjects()
			return nil, err
		}
		files, membershipChanged, restoreErr := s.restorePyPIVersionLocked(objectCtx, repositoryID, project, version, objectKeys)
		releaseDistribution()
		releaseObjects()
		if membershipChanged {
			continue
		}
		return files, restoreErr
	}
	return nil, ErrUpstreamChanged
}

func (s *PostgresStore) deletedPyPIObjectKeys(ctx context.Context, repositoryID, project, version string) ([]string, bool, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT object_key,collected_at IS NOT NULL
		FROM native_pypi_files WHERE repository_id::text=$1 AND project=$2 AND version=$3 AND state='deleted'
		ORDER BY object_key`, repositoryID, project, version)
	if err != nil {
		return nil, false, false, err
	}
	objectKeys := make([]string, 0)
	seen := make(map[string]bool)
	found, collected := false, false
	for rows.Next() {
		var objectKey string
		var isCollected bool
		if err = rows.Scan(&objectKey, &isCollected); err != nil {
			_ = rows.Close()
			return nil, false, false, err
		}
		found = true
		collected = collected || isCollected
		if objectKey != "" && !seen[objectKey] {
			seen[objectKey] = true
			objectKeys = append(objectKeys, objectKey)
		}
	}
	if err = rows.Close(); err != nil {
		return nil, false, false, err
	}
	return objectKeys, found, collected, nil
}

func (s *PostgresStore) restorePyPIVersionLocked(ctx context.Context, repositoryID, project, version string, expectedObjectKeys []string) ([]PyPIFile, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT object_key,collected_at IS NOT NULL FROM native_pypi_files
		WHERE repository_id::text=$1 AND project=$2 AND version=$3 AND state='deleted' FOR UPDATE`, repositoryID, project, version)
	if err != nil {
		return nil, false, err
	}
	currentObjectKeys := make([]string, 0)
	seen := make(map[string]bool)
	found, collected := false, false
	for rows.Next() {
		var objectKey string
		var isCollected bool
		if err = rows.Scan(&objectKey, &isCollected); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		found = true
		collected = collected || isCollected
		if objectKey != "" && !seen[objectKey] {
			seen[objectKey] = true
			currentObjectKeys = append(currentObjectKeys, objectKey)
		}
	}
	if err = rows.Close(); err != nil {
		return nil, false, err
	}
	if !found || collected {
		return nil, false, ErrDisabled
	}
	sort.Strings(currentObjectKeys)
	if !slices.Equal(expectedObjectKeys, currentObjectKeys) {
		return nil, true, nil
	}
	rows, err = tx.QueryContext(ctx, `UPDATE native_pypi_files SET state='visible',deleted_at=NULL
		WHERE repository_id::text=$1 AND project=$2 AND version=$3 AND state='deleted' AND collected_at IS NULL
		RETURNING `+pypiFileColumns, repositoryID, project, version)
	if err != nil {
		return nil, false, err
	}
	files := make([]PyPIFile, 0)
	for rows.Next() {
		var file PyPIFile
		if err = scanPyPIFile(rows, &file); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		files = append(files, file)
	}
	if err = rows.Close(); err != nil {
		return nil, false, err
	}
	if len(files) == 0 {
		return nil, false, ErrDisabled
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM artifact_tombstones WHERE repository_id::text=$1 AND format='pypi' AND coordinate=$2`, repositoryID, project+"@"+version); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return files, false, nil
}

func (s *PostgresStore) ListReclaimablePyPIObjects(ctx context.Context, before time.Time, limit int) ([]PyPIObject, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT min(repository_id::text),object_key,min(digest),max(size),max(deleted_at)
		FROM native_pypi_files d WHERE state='deleted' AND collected_at IS NULL AND object_key<>''
		AND NOT EXISTS (SELECT 1 FROM native_pypi_files v WHERE v.object_key=d.object_key AND v.state='visible')
		GROUP BY object_key HAVING max(deleted_at)<$1 ORDER BY object_key LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	objects := make([]PyPIObject, 0)
	for rows.Next() {
		var object PyPIObject
		if err = rows.Scan(&object.RepositoryID, &object.ObjectKey, &object.Digest, &object.Size, &object.DeletedAt); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func (s *PostgresStore) PyPIObjectHasVisibleReference(ctx context.Context, objectKey string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_pypi_files WHERE object_key=$1 AND state='visible')`, objectKey).Scan(&referenced)
	return referenced, err
}

func (s *PostgresStore) MarkPyPIObjectCollected(ctx context.Context, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_pypi_files SET collected_at=now()
		WHERE object_key=$1 AND state='deleted' AND collected_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM native_pypi_files v WHERE v.object_key=$1 AND v.state='visible')`, objectKey)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

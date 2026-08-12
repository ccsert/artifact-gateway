package repository

import "context"

func (s *PostgresStore) LockArtifactObjectKeys(ctx context.Context, format Format, objectKeys []string) (context.Context, func(), error) {
	var prefix string
	switch format {
	case FormatRaw:
		prefix = "native-raw-object:"
	case FormatNPM:
		prefix = "native-npm-object:"
	case FormatPyPI:
		prefix = "native-pypi-object:"
	case FormatAPT:
		prefix = "native-apt-object:"
	default:
		return ctx, nil, ErrDisabled
	}
	keys := make([]string, 0, len(objectKeys))
	for _, objectKey := range objectKeys {
		if objectKey != "" {
			keys = append(keys, prefix+objectKey)
		}
	}
	return s.lockPostgresAdvisoryKeys(ctx, keys)
}

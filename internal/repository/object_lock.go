package repository

import (
	"context"
	"sort"
)

// LockObjectKeys acquires object locks in a stable order and releases any
// partial acquisition on failure. Stable ordering prevents multi-file formats
// from deadlocking when two workers coordinate the same object set.
func LockObjectKeys(ctx context.Context, objectKeys []string, lock func(context.Context, string) (func(), error)) (func(), error) {
	unique := make(map[string]struct{}, len(objectKeys))
	keys := make([]string, 0, len(objectKeys))
	for _, key := range objectKeys {
		if key == "" {
			continue
		}
		if _, exists := unique[key]; exists {
			continue
		}
		unique[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	releases := make([]func(), 0, len(keys))
	for _, key := range keys {
		release, err := lock(ctx, key)
		if err != nil {
			for index := len(releases) - 1; index >= 0; index-- {
				releases[index]()
			}
			return nil, err
		}
		releases = append(releases, release)
	}
	return func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}, nil
}

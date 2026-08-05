package app

import (
	"context"
	"errors"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// RepositoryDeletionWorker advances repositories through the asynchronous
// management state machine. The terminal deleted row is retained as metadata
// so repository references and audit history remain addressable.
type RepositoryDeletionWorker struct {
	Store repository.HostedRepositoryStore
}

func (w RepositoryDeletionWorker) Run(ctx context.Context) (int, error) {
	if w.Store == nil {
		return 0, nil
	}
	finalized := 0
	var after string
	seen := make(map[string]struct{})
	for {
		repositories, next, err := w.Store.ListHostedRepositories(ctx, 200, after)
		if err != nil {
			return finalized, err
		}
		for _, repo := range repositories {
			if repo.State != repository.RepositoryDeleting {
				continue
			}
			if _, err = w.Store.FinalizeHostedRepositoryDeletion(ctx, repo.ID); err != nil {
				if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrVersionConflict) {
					continue
				}
				return finalized, err
			}
			finalized++
		}
		if next == "" {
			return finalized, nil
		}
		if _, ok := seen[next]; ok {
			return finalized, errors.New("repository deletion pagination cursor repeated")
		}
		seen[next] = struct{}{}
		after = next
	}
}

func (w RepositoryDeletionWorker) Start(ctx context.Context, interval time.Duration) {
	if w.Store == nil || interval <= 0 {
		return
	}
	go func() {
		_, _ = w.Run(ctx)
		wake := notificationWake(ctx, w.Store, "artifact_gateway_repository_deletions")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = w.Run(ctx)
			case <-wake:
				_, _ = w.Run(ctx)
			}
		}
	}()
}

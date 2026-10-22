package main

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/ccsert/artifact-gateway/internal/config"
	presetpg "github.com/ccsert/artifact-gateway/internal/repositorypreset/pgstore"
)

func initializeRepositoryPresets(cfg config.Config, db *sql.DB) error {
	if cfg.RepositoryPresetsMode != "standard" || !cfg.HasRole(config.NodeRoleAPI) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := presetpg.New(db).Initialize(ctx)
	if err != nil {
		return err
	}
	slog.Info("repository presets initialized", "state", result.State, "catalog", result.Version,
		"repositories_created", result.Repositories, "groups_created", result.Groups)
	return nil
}

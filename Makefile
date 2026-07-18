.DEFAULT_GOAL := help

COMPOSE := docker compose --env-file .env -f compose.gitea.yml

.PHONY: help gitea-up gitea-down gitea-reset gitea-seed gitea-fixture

help:
	@printf '%s\n' 'Targets: gitea-up, gitea-down, gitea-reset, gitea-seed, gitea-fixture'

gitea-up:
	@./scripts/gitea-up.sh

gitea-down:
	@$(COMPOSE) down

gitea-reset:
	@./scripts/gitea-reset.sh

gitea-seed:
	@./scripts/gitea-seed.sh

gitea-fixture: gitea-up gitea-seed


# AGENTS.md

govd is a single-binary Go Telegram bot that downloads media from social platforms. Go 1.26, module `github.com/govdbot/govd`. No tests, no Makefile, no task runner.

## Setup & commands

- After changing `internal/database/queries/*.sql` or `internal/database/migrations/*.sql`, run `sqlc generate` (sqlc v1.30.0, config in `sqlc.yaml`). Air and the `Dockerfile` run it before every build. Never hand-edit `internal/database/*_gen.go` (sqlc-generated).
- Build needs CGO for libheif: `CGO_ENABLED=1 go build -o govd ./cmd/main.go`. `ffmpeg` must be on `PATH` at runtime (fatal check in `cmd/main.go`).
- Lint: `golangci-lint run --build-tags=lint`. The `lint` tag excludes `internal/util/heif.go` (`//go:build !lint`), which is the CGO libheif import, so lint works without libheif installed.
- Local dev: `docker compose -f docker-compose.dev.yaml up --build`. Requires a `.env` (see `.env.example`; `BOT_TOKEN` is required). Air hot-reloads via `.air.toml`.
- There are no test files. Verify changes by building and running the dev compose stack.

## Architecture

- Entrypoint `cmd/main.go`: logger → `config.Load()` → ffmpeg check → optional pprof/prometheus servers → `localization.Init()` → `database.Init()` (auto-runs embedded goose migrations) → `bot.Start()` (long polling, blocks).
- `internal/config`: env vars parsed in `env.go` / `parser.go`, defaults in `GetDefaultConfig()`. Per-extractor settings (proxy, `disabled`, `ignore_regex`, youtube `instance`) come from `private/config.yaml`, which is gitignored; schema in `internal/config/models.go`, example in `private/config-example.yaml`.
- `internal/extractors`: one package per site. Adding or changing an extractor requires registering its `*models.Extractor` values in the `Extractors` slice in `internal/extractors/main.go`. Matching is by `Host` + `URLPattern` regex.
- `internal/core`: download orchestration, DB-backed caching, album limits, Telegram sending.
- `internal/plugins`: post-download transforms (`id3`, `merge_audio`).
- `internal/database`: schema changes go in a new numbered `migrations/000NN_*.sql`, queries in `queries/*.sql`, then `sqlc generate`. Migrations run automatically at startup.

## i18n

- Messages are declared in `internal/localization/messages.go`; translations live in `internal/localization/locales/active.<lang>.toml`.
- Adding a language requires both the new TOML file and a matching `mustLoad(...)` line in `internal/localization/main.go` (locales are embedded and loaded explicitly).
- Adding/renaming messages: edit `messages.go`, then use the `goi18n` CLI to extract/merge. Fixing an existing translation can be done directly in the TOML.

## CI

PRs run `golangci-lint` (`--build-tags=lint`), `sqlc vet`, and hadolint on both Dockerfiles. Multi-arch (amd64/arm64) images are pushed to Docker Hub + GHCR only on pushes to `main`.

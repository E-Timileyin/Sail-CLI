# Sail — Onboarding Guide

Welcome. This guide gets you from a fresh clone to running a deploy, and points you at the
parts of the codebase you'll touch first. Read `docs/ARCHITECTURE.md` alongside this.

---

## 1. What Sail is (in one paragraph)

Sail is a Go CLI that deploys Dockerized applications to remote servers over SSH, with
automatic backup and rollback. It sits between "manual `docker-compose` over SSH" and a
full CI/CD platform: push-button, repeatable, rollback-safe deploys for a handful of
servers, with nothing to install on the target beyond Docker.

---

## 2. Prerequisites

- **Go 1.25+** (see `go.mod`)
- **Docker** running locally (needed for the SDK-based code paths and tests that touch it)
- **SSH access** to at least one target server for real deploys
- On target servers: **Docker + Docker Compose** installed

---

## 3. First run

```bash
# Clone and enter
git clone https://github.com/E-Timileyin/Sail.git
cd Sail

# Build
go build -o sail .

# See available commands
./sail --help

# Run the test suite
go test -v ./...
```

Install as a CLI (optional):

```bash
go install github.com/E-Timileyin/sail@latest   # puts `sail` in $GOPATH/bin
```

---

## 4. Configuration files

Sail reads a YAML config describing your servers (and, for the Orchestrator path,
deployment settings).

### `config.yaml` — servers

```yaml
app:
  name: Sail
  environment: development

deployment:
  container_name: sail-api
  dockerfile: ./Dockerfile
  port: 8080

servers:
  - name: production
    host: your-server-ip
    port: 22
    user: deploy
    key_path: ~/.ssh/your_private_key   # preferred
    # password: ...                      # fallback (avoid committing real secrets)
```

> **Security note:** the example config ships with a plaintext `password:`. Do **not** commit
> real credentials. Prefer `key_path`. See `docs/fixes/05-secrets-handling.md`.

### `deployment.yaml` — application settings (Orchestrator path)

```yaml
image: your-docker-image
tag: latest
container_name: my-app
ports:
  "8080": "80"
environment:
  NODE_ENV: production
restart_policy: unless-stopped   # always | unless-stopped | on-failure | no
health_check:
  test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
  interval: 2s
  timeout: 30s
  retries: 5
```

---

## 5. Common commands

```bash
# Deploy to every server in the config
./sail deploy config.yaml

# SSH into a named server (optionally run a one-off command)
./sail ssh production
./sail ssh production "docker ps"

# Start the app server locally (placeholder today)
./sail serve --port 8080

# Global flags (work on any command)
./sail deploy config.yaml --log-level debug --log-format json
```

> `--dry-run` prints the plan and changes nothing. `--accept-new` records an unknown host
> key in `known_hosts` on first connect (OpenSSH's `StrictHostKeyChecking=accept-new`).
> `--skip-backup` and `--force-rebuild` were removed — see
> `docs/fixes/01-exit-codes-and-flags.md`.

---

## 6. Codebase tour — where to look first

| You want to…                                  | Start in…                                      |
|-----------------------------------------------|------------------------------------------------|
| Add or change a CLI command                   | `cmd/` (each command is one file)              |
| Change how config is loaded                   | `internal/config/loader.go`                    |
| Change server/deploy data shapes              | `internal/model/`                              |
| Change how Docker is driven (SDK)             | `internal/container/` (preferred) or `internal/docker/` |
| Change deploy/rollback logic                  | `internal/workflows/orchestrator.go`           |
| Change logging                                | `internal/logger/`                             |

### The mental model

- `cmd/deploy.go` is the **live** deploy path: SSH in, run `docker-compose` remotely.
- `internal/workflows` + `internal/docker` is a **richer SDK-based** deploy with real
  rollback — but it isn't wired into the CLI yet.
- `internal/container/` is the **target refactor**: clean interfaces, dependency injection,
  and unit tests. New Docker work should go here.

Read `docs/ARCHITECTURE.md` §5–6 to understand this three-way split before making changes.

---

## 7. How to test your changes

```bash
go test ./...                          # everything
go test ./internal/container/...       # a single package
go test -run TestName ./internal/...   # a single test
```

The `container/service` package is designed for unit testing via
`NewContainerServiceWithClient(mock)` — inject a fake `DockerClient` and assert behavior
without a real Docker daemon. Mirror that pattern for new service code.

---

## 8. Contributing checklist

- [ ] New Docker functionality goes in `internal/container/` behind an interface.
- [ ] Wrap errors with `%w` so callers can `errors.Is/As`.
- [ ] Thread `context.Context` through anything that does I/O.
- [ ] Inject the `logrus.Logger` rather than reaching for a global.
- [ ] Add/adjust tests; run `go test ./...` and `go vet ./...`.
- [ ] Don't commit real secrets. Prefer SSH keys over passwords.

See `CONTRIBUTING.md` for the PR process, and `docs/fixes/` for the current improvement
roadmap if you're looking for a first task.

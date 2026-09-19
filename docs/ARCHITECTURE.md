# Sail — Architecture

> A Go CLI that deploys Dockerized applications to remote servers over SSH, with backup/rollback.

This document describes how Sail is structured today, the patterns it uses, and the
in-progress refactor that shapes the codebase. It is the reference for anyone extending
the tool.

---

## 1. High-level picture

Sail is a **single binary** run from an operator's machine. It reads YAML config, connects
to one or more servers, and drives Docker on them. Nothing is installed on the target
server beyond Docker itself.

```
┌─────────────────────────────────────────────────────────┐
│  cmd/            Cobra commands — the CLI surface          │  user-facing
├─────────────────────────────────────────────────────────┤
│  internal/workflows          deploy/rollback orchestration │  use cases
│  internal/container/service  service layer (interface-based)│
├─────────────────────────────────────────────────────────┤
│  internal/container/client   Docker SDK adapter            │  infrastructure
│  internal/docker             Docker SDK wrapper (Manager)  │
├─────────────────────────────────────────────────────────┤
│  internal/model   internal/config   internal/logger        │  shared kernel
└─────────────────────────────────────────────────────────┘
```

Dependencies point **inward**: `cmd` depends on services/orchestration, which depend on
interfaces, with the Docker SDK sitting behind those interfaces at the outer edge.

---

## 2. Entry flow

```
main.go
  └─ cmd.Execute()                      // cmd/root.go
       └─ Cobra parses args
            ├─ PersistentPreRunE         // initializes the logger once, for every command
            └─ dispatches to a subcommand RunE
```

`main.go` is 7 lines and delegates entirely to `cmd`. Each subcommand file registers
itself onto `rootCmd` inside an `init()` function, so `main` never needs to know which
commands exist.

---

## 3. Commands (`cmd/`)

| File          | Command                    | What it does                                                            |
|---------------|----------------------------|-------------------------------------------------------------------------|
| `root.go`     | `sail`                     | Root command; sets up global `--log-level` / `--log-format` flags       |
| `deploy.go`   | `sail deploy <config>`     | Connects to each server via SSH and runs `docker-compose` remotely      |
| `serve.go`    | `sail serve`               | Placeholder HTTP server; `--deploy` chains a deploy first (stubbed)     |
| `ssh.go`      | `sail ssh <name> [cmd]`    | Opens an interactive SSH session to a named server (shells out to `ssh`)|

### The `deploy` path (what actually ships)

`runDeploy` (`cmd/deploy.go`) iterates every server and:

1. Loads config → `[]model.ServerStruct`
2. Builds SSH auth via `ServerStruct.SSHConfig()` — **key first, password fallback**
3. `ssh.Dial` to connect
4. Verifies `docker` + `docker-compose` exist on the remote
5. Runs the fixed sequence **on the server**:
   `docker-compose pull` → `down` (error-tolerant) → `up -d`
6. Verifies with `docker ps`

On per-server failure the loop **`continue`s** — fail-soft across a fleet.

---

## 4. Configuration (`internal/config`, `internal/model`)

- `LoadConfig` uses **Viper** to read a YAML file and `godotenv` to load `.env` (optional).
- `viper.UnmarshalKey("servers", ...)` maps the `servers:` block onto `[]model.ServerStruct`.
- `model.ServerStruct` — connection target (host/port/user + key or password).
- `model.Deployment` — image/tag, ports, env, restart policy, health check (used by the
  Orchestrator path).

`ServerStruct.SSHConfig()` builds an ordered list of `ssh.AuthMethod` (Strategy pattern)
and returns a client config. **Note:** it currently uses `ssh.InsecureIgnoreHostKey()` —
see `docs/fixes/02-host-key-verification.md`.

---

## 5. Docker layers — current state is NOT the target

> **Read this before trusting the layout below.** There are **three** overlapping Docker
> abstractions. The previous version of this document called that "deliberate… mid-migration".
> That was false: nothing is migrating between them. They coexist, two of the three are
> dead code, and none of them run against a remote server. The adopted target is
> [`docs/adr/0001-target-architecture.md`](adr/0001-target-architecture.md).

| # | Location                       | Style                                  | Status                              |
|---|--------------------------------|----------------------------------------|-------------------------------------|
| 1 | `cmd/deploy.go`                | SSH + shell `docker-compose`           | **Ships today** (CLI uses this)     |
| 2 | `internal/docker/Manager`      | Direct Docker SDK, concrete struct     | Dead — Orchestrator unreachable from CLI |
| 3 | `internal/container/*`         | Direct Docker SDK, interface-based     | Dead — wired into nothing           |

### #1 — `cmd/deploy.go` (the only path that executes)

Dials the server over SSH and runs a fixed `docker-compose` sequence as shell strings, then
verifies with `docker ps`. This is the pre-ADR architecture. It is being replaced, not
extended.

### #2 — `internal/docker/Manager` (dead)

A concrete wrapper over the Docker Engine SDK. Used only by `internal/workflows`. Connects
via `client.FromEnv` — i.e. the **operator's local socket**, so even if it were wired up it
would deploy to the wrong machine. Its remote-SSH successor was proposed in
`docs/fixes/03` and **does not work** (see ADR 0001 §Verification).

### #3 — `internal/container/` (dead, and not the target)

The old "intended design": SDK adapter + service layer with a mock-friendly `DockerClient`
interface. Its `types.ContainerManager` interface (`Deploy`/`Rollback`/`GetStatus`/`GetLogs`)
is a reasonable shape, but the *implementation strategy* underneath it is the one ADR 0001
rejects. To be deleted during the restructure, after its ideas are carried into the
compose-based design.

### Target

`docker compose` on the server, driven by a forced-command `sail agent`. One Docker
abstraction, not three. See ADR 0001.

---

## 6. Orchestration (`internal/workflows`) — to be retired

`Orchestrator` composes `Manager` primitives into a multi-step deploy **with compensation
on failure** — a lightweight Saga:

```
validate → check docker → pull image → backup existing (rename → *-backup)
        → create + start new → verify → success: remove backup
                                   │
                              fail → Rollback(): remove new, restore *-backup, restart
```

**This code is unreachable from the CLI and is being retired under ADR 0001**, not adapted:
it is built on the SDK container API, which the target architecture does not use. The
rename-to-backup idea is worth preserving in spirit; rollback in the target is re-expressed
as tag switching plus `docker compose up -d --wait`.

The rename-to-backup trick synthesizes atomicity Docker doesn't provide natively: the old
container stays alive under a temp name until the new one proves healthy.

> **Important:** the Orchestrator is **not yet wired into any CLI command**, and it talks to
> the **local** Docker daemon (`FromEnv`), not the remote server. It is retired under ADR
> 0001 — see the section above.

---

## 7. Cross-cutting patterns

| Pattern                    | Where                                   | Notes                                             |
|----------------------------|-----------------------------------------|---------------------------------------------------|
| Command pattern            | `cmd/*` via Cobra                        | Self-registering commands in `init()`             |
| Dependency Inversion + DI  | `internal/container/service`            | Consumer-defined interface; inject mock in tests  |
| Adapter / ACL              | `internal/container/client`             | Docker SDK types → `model.*`                       |
| Repository / Manager       | `internal/docker`, `container/service`  | Hides "how containers are managed"                |
| Orchestrator / Saga        | `internal/workflows`                    | Multi-step deploy with rollback compensation      |
| Strategy                   | `ServerStruct.SSHConfig()`              | Key-first, password-fallback auth methods         |
| Error wrapping             | everywhere                              | `fmt.Errorf("...: %w", err)` for `errors.Is/As`   |
| Context propagation        | all Docker/workflow methods             | `ctx` first arg for cancellation/timeouts         |
| Structured logging (DI'd)  | `logrus.Logger` passed into components  | Logger injected, not global, in newer code        |
| Config as data             | `internal/config` + Viper + struct tags | Declarative YAML → structs                        |

---

## 8. Known gaps (see `docs/fixes/`)

Fixed in the current tree:

- ✅ `ssh.InsecureIgnoreHostKey()` removed from both sites; host keys are verified against
  `known_hosts`, with opt-in `--accept-new`. Guarded by
  `internal/sshx/guard_test.go`, which fails the build on reintroduction.
- ✅ `runDeploy` returns an aggregate error, so a failed deploy exits non-zero.
- ✅ `--dry-run` performs no connection or mutation.
- ✅ `--skip-backup` and `--force-rebuild` removed (neither had an effect).
- ✅ `password:` is rejected by the loader; config schema is versioned at 2.

Still open:

- Orchestrator + real rollback not wired into the CLI (SSH+compose path ships instead).
- `verifyDeployment` is shallow (`docker ps`), not the container's `HEALTHCHECK`.
- Secret resolution (`apps/<name>/.env` on the server) is designed but not implemented.
- `sail agent`, `bootstrap`, `app new/rm`, `rollback`, `logs`, `history`, `init` absent.

Status of the fix write-ups is tracked in `docs/fixes/README.md`. Note that
`docs/fixes/03` is **superseded** — its proposal does not work. The adopted target is
[`docs/adr/0001-target-architecture.md`](adr/0001-target-architecture.md).

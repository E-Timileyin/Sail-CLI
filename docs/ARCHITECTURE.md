# Sail — Architecture

A Go CLI that deploys Dockerized applications to a remote server over SSH, by driving
`docker compose` on the target with rollback on failure.

Current behaviour is defined by the code and the ADRs. Where they disagree, the ADRs win,
because they record why:

| ADR | Decision |
|-----|----------|
| [0001](adr/0001-target-architecture.md) | Drive `docker compose` on the target via a forced-command agent, not the Docker SDK over `ssh://` |
| [0002](adr/0002-rollback-design.md) | Rollback by switching the image tag, not by renaming containers |

---

## 1. Layout

```
cmd/               Cobra commands. Parse flags, call down. No SSH, no Docker.
internal/domain/   Server, Deployment, TrustPolicy. Standard library only.
internal/sshx/     The only place an ssh.ClientConfig is built.
internal/remote/   Deploy plan, deployer, runner, compose templates.
internal/config/   Load, version-check, and validate config.
internal/logger/   logrus setup.
```

Dependencies point inward. `domain` imports nothing from the project, so a domain type
cannot open a socket — which is why building an SSH client lives in `sshx` rather than on
`Server`.

## 2. What runs where

Sail runs from the operator's machine. On the server, an app is a directory:

```
<srv-root>/<app>/
  compose.yaml   image reference, ports, healthcheck
  .env           secrets, chmod 600. Sail references it, never writes secrets into it.
  .tag           currently deployed tag
  .tag.prev      previous tag — the rollback target
  .env.tag       written per deploy, holds APP_TAG
  deploy.log     append-only history
```

There is no daemon and no agent process. Each command opens one SSH connection, runs
what it needs, and exits.

## 3. The deploy path

`cmd/deploy.go` → `internal/remote.Deployer.Deploy`:

```
preflight (docker + compose v2) → read state → validate → write tag
    → compose pull → compose up -d --wait ─┬─ healthy   → promote
                                           └─ unhealthy → roll back
```

Two properties matter more than the rest:

- **`up -d --wait` is the health gate.** It blocks on the container's own `HEALTHCHECK` and
  exits non-zero if it never passes. Sail does not reimplement health checks. This is why
  `PreflightScript` rejects compose v1: v1 has no `--wait`, so accepting it would silently
  remove the gate.
- **Promote happens only after the gate passes.** `.tag.prev` is written first, then
  `.tag`. A failed deploy therefore leaves the previous tag recorded as current, which is
  what makes rollback meaningful. Details and the rejected alternatives: ADR 0002.

`DeployScript` and friends are pure functions returning strings. The deploy plan is data,
so the whole sequence — including the rollback path — is testable without SSH or Docker.

## 4. SSH trust

`internal/sshx` owns the policy and is the only constructor of `ssh.ClientConfig`.
`internal/remote.Dial` is the only dial path.

Host keys are verified against `known_hosts`. `--accept-new` records an unknown host on
first use (OpenSSH `accept-new`). A key that does not match a recorded entry is refused
under **both** policies: trust-on-first-use may add a key, never replace one.

`ssh.InsecureIgnoreHostKey()` is banned, and `internal/sshx/guard_test.go` fails the build
if it returns. It appeared in two places once and survived unnoticed behind tests that
skipped without a live server.

## 5. Configuration

`config.yaml` requires `version: 2` and holds no secrets:

```yaml
version: 2
app:
  name: Sail
  environment: production
servers:
  - name: production
    host: 1.2.3.4
    user: deploy
    key_path: ~/.ssh/id_ed25519
```

Two notes for anyone touching this:

- **Both `yaml` and `mapstructure` tags are required on config structs.** Viper's
  `Unmarshal` reads `mapstructure`, not `yaml`. With yaml-only tags, multi-word keys such
  as `key_path` silently unmarshal as empty — single-word keys appear to work because
  Viper fuzzy-matches them. v0.1.0 shipped that bug, and it is why key auth never worked.
- **A version bump is a clean break.** The loader rejects an undeclared or wrong version
  with an actionable message rather than reinterpreting it. A permissive loader is how a
  plaintext password field survives unnoticed.

## 6. Testing

All tests run without a server: in-process SSH servers for the transport, a fake executor
for the deploy state machine, and pure-function tests for the plan and templates.

```bash
go test ./... -race
```

No test skips. Earlier versions skipped when a live SSH server was absent, which meant
they asserted nothing while reporting coverage.

## 7. Not implemented

Listed so the gap is visible rather than implied. Tracked as GitHub issues.

- **`sail agent`** — the forced-command server-side role from ADR 0001. Blocked on what a
  leaked agent key may redeploy (see the ADR's open question).
- **`sail bootstrap`, `sail init`** — host hardening and project scaffolding.
- **`sail serve`** — a stub. `--deploy` chains a deploy; it never listens.
- **GoReleaser and release assets** — v0.1.0 shipped no binaries, and its documented
  `go install` path does not resolve because the module path and repo URL disagree.
- **Reconciliation of `.tag` files against `docker compose ps`** — a hand-edit on the
  server currently diverges silently.

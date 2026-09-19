# ADR 0001 — Sail drives `docker compose` on the target, via a forced-command agent

- **Status:** Accepted
- **Date:** 2025-11-23
- **Supersedes:** `docs/fixes/03-unify-docker-daemon.md`
- **Deciders:** project owner

## Context

`fixes.md` and `docs/fixes/03-unify-docker-daemon.md` describe two different target
architectures for the same problem, and both cannot hold:

- **`fixes.md`:** Sail drives `docker compose` on the target server, using
  `up -d --wait` for the health gate. A single binary serves both roles: a
  client-side CLI and a server-side `sail agent` pinned via `authorized_keys`
  forced-command. The config shrinks to roughly `app`, `image`, `server`, `domain`,
  `port`; the compose file on the server carries healthchecks, resource limits and
  networks.
- **`docs/fixes/03`:** point the Docker Go SDK at the remote daemon by constructing a
  client with host `ssh://user@host:port`, so `internal/workflows.Orchestrator` runs
  unchanged against production.

The second is the smaller change to the existing code, so it is the tempting one. It
does not work.

### Verification

`docs/fixes/03` proposes:

```go
host := fmt.Sprintf("ssh://%s@%s:%d", s.User, s.Host, s.Port)
cli, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
```

Checked against the module this repository actually depends on
(`github.com/docker/docker v23.0.0+incompatible`, per `go.mod`):

- `client.WithHost` is documented as: *"WithHost overrides the client host with the
  specified one."* Nothing more. There is no SSH dialling in `client/options.go`.
- The package that interprets an `ssh://` URL and shells out to the `docker` CLI binary
  is `cli/connhelper/ssh`, in module **`github.com/docker/cli`**. That module is not a
  dependency of this project — it does not appear in `go.sum`.
- `client.NewClientWithOpts` builds an HTTP client addressed by a host string.

The practical consequence: the snippet above yields an HTTP client pointed at the literal
host string `ssh://deploy@example.com:22`. It does not open an SSH connection, and it
fails at first request. `ssh://` is a **Docker CLI** feature, not a Docker Engine SDK
feature.

Adopting the SDK-over-`ssh://` design would additionally require either depending on
`github.com/docker/cli` (pulling the CLI's connhelper machinery into a deployment tool to
reach a daemon) or hand-writing an SSH-backed `net.Conn` dialer against the Docker
socket. The first is a large dependency for no gain; the second is reimplementing
`connhelper`, which is the brittle work the fix was trying to avoid.

## Decision

Sail targets the `fixes.md` architecture:

1. **The server runs `docker compose`.** Sail ships or updates a compose file plus an
   `.env` on the server and runs `docker compose up -d --wait`. Health gating is the
   container's own `HEALTHCHECK`; Sail does not reimplement health checks.
2. **One binary, two roles.** `sail agent <verb>` is the server-side role. A forced-command
   SSH key in `authorized_keys` pins that role, so a leaked key cannot obtain a shell.
3. **Transport is `ssh <host> "<command>"`**, not a remote Docker socket. Commands are
   constrained to the agent's verb set.
4. **No daemon-side credentials.** Sail never needs Docker socket access from the
   operator's machine for a remote deploy.

## Consequences

**Positive**

- The health signal is the container's real `HEALTHCHECK`, which is what makes rollback
  meaningful. No bespoke probe code to maintain.
- `docker compose` already expresses healthchecks, memory limits, per-project networks,
  DB containers and ordering — none of which `docker run` or the current
  `model.Deployment` schema can express.
- The forced-command key reduces the blast radius of a leaked CI key from "shell on the
  production host" to "run the agent's verbs".
- No dependency on `github.com/docker/cli`.
- Testable without a daemon: the agent's verbs are the seam.

**Negative / accepted costs**

- **Password-only servers stop working.** The transport is SSH key based. Combined with
  the removal of the plaintext `password:` config field, password auth is gone by design.
- **`internal/workflows.Orchestrator` is retired,** not adapted. Its rename-to-backup
  saga is valuable logic, but it is built on the SDK container API. Rollback is
  re-expressed as tag switching plus `up -d --wait` (see `fixes.md`). This is a real loss
  of working code and is called out rather than glossed.
- The agent's authorization model is a new surface. What a leaked CI key can redeploy must
  be scoped deliberately — tracked as an open decision, not assumed.
- Migration is a rewrite, not a patch. Sequencing is per `fixes.md`: do not block projects
  on it; port each piece as it proves out.

## Open decision (not resolved by this ADR)

**What may a forced-command agent key redeploy?** If the pinned command is
`sail agent deploy <app> <tag>` with unrestricted `<app>` and `<tag>`, a leaked CI key can
redeploy *any* app to *any* tag under the deploy user — including a tag that was never
built by CI. Options to be decided when `sail agent` is implemented: pin the app name in
`authorized_keys` (one key per app, strongest), validate that the tag exists in the
registry, or both. Deliberately left open; do not implement without a decision.

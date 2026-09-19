# Sail — Architecture

Extending **Sail** from a one-shot deployment CLI into a lightweight,
self-hosted supervisor + alerter for a single VPS.

> **Where Sail is today:** a Go/Cobra/Viper CLI that deploys Dockerized apps to a
> remote server over SSH (`deploy`, `serve`, `ssh`), with rollback-on-failure and
> `servers.yaml` / `deployment.yaml` config. It runs, acts, and exits.
>
> **Where this takes it:** add continuous supervision — auto-restart crashed
> containers, aggregate their logs, and alert to Discord/email when something
> breaks — while keeping the existing deploy flow intact.

---

## 1. The one decision everything hangs on

Sail today is a **one-shot process**: you run it, it SSHes in, deploys, exits.
That model **cannot** supervise, auto-restart, or watch for crashes — because
nothing is running on the server when Sail isn't. A container that dies at 3am
has no one watching it.

So the extension is **not** "more Cobra commands." It's a second component:

- **`sail`** — the CLI you already have. The **control plane**. Runs on your
  machine, issues commands, exits. Stays a Cobra/Viper CLI.
- **`sail-agent`** — a new **long-running daemon** installed on the VPS. The
  **data plane**. Watches containers via the Docker API, restarts them, collects
  logs, fires alerts. Never exits.

```
   your laptop                        your VPS
 ┌────────────┐    SSH / HTTP     ┌──────────────────────────┐
 │    sail    │ ────────────────► │        sail-agent        │
 │  (CLI)     │   deploy, status  │  (daemon, always running)│
 │  Cobra/Viper│ ◄──────────────── │                          │
 └────────────┘     status/logs   │  supervises containers    │
                                   │  via Docker API           │
                                   └──────────────────────────┘
                                              │ Docker API (/var/run/docker.sock)
                                              ▼
                                   ┌──────────────────────────┐
                                   │  your app containers      │
                                   └──────────────────────────┘
```

This CLI-talks-to-resident-agent split is exactly how Dokku, Nomad, and
Kubernetes are shaped. It's the correct design *and* the one that teaches you the
most — process supervision only makes sense once you have a process that stays
alive.

**How they communicate:** the agent exposes a small **local HTTP API** bound to
`127.0.0.1` on the VPS. `sail` reaches it by tunneling over the **SSH connection
Sail already establishes** (local port-forward). No new public port, no new auth
system — you reuse the SSH trust you already have. This is the single most
important reuse decision in the design: the agent's API is never exposed to the
internet; the only way to reach it is through SSH you already hold keys for.

---

## 2. Component responsibilities

### `sail` (existing CLI — extended, not rewritten)

Keeps everything it does now. Gains commands that talk to the agent:

| Command | What it does |
|---------|--------------|
| `sail deploy` | *(existing)* deploy an app — now also registers it with the agent for supervision |
| `sail agent install <server>` | installs + starts `sail-agent` on the VPS (as a systemd unit) |
| `sail status [server]` | asks the agent: what's running, what's down, recent restarts |
| `sail logs <app>` | streams aggregated logs for an app from the agent |
| `sail ps` | live view of supervised containers and their health |

The CLI never talks to Docker directly for supervision — it asks the agent. That
boundary is what keeps the two components cleanly separable.

### `sail-agent` (new daemon)

Four subsystems, one process:

1. **Supervisor** — watches supervised containers, restarts them on crash /
   health-check failure, with backoff and flap detection.
2. **Log collector** — streams each container's stdout/stderr, buffers and
   stores it, serves it back to `sail logs`.
3. **Alerter** — on a state transition worth caring about (down, repeated
   restarts, recovered), dispatches to Discord and/or email.
4. **API** — the local HTTP surface `sail` calls (`/status`, `/logs`, `/apps`).

---

## 3. Package architecture (extends your existing `cmd/` + `internal/`)

Sail already uses `cmd/` + `internal/`. Keep that. Organize the new work
**by component**, the idiomatic Go way and the way real deploy systems are laid
out — not by Clean-Architecture layers. Deployment tooling's complexity is
technical (Docker API, supervision loops, log streaming), not domain-modeling, so
package-by-component fits and package-by-layer would just add ceremony.

```
Sail/
  main.go                        # (existing) root entry
  cmd/                           # (existing) Cobra commands — control plane
    root.go
    deploy.go                    # (existing) — extend to register app w/ agent
    serve.go                     # (existing)
    ssh.go                       # (existing)
    agent.go                     # NEW: `sail agent install/status`
    status.go                    # NEW: `sail status`
    logs.go                      # NEW: `sail logs <app>`
  internal/
    # ── existing deploy-side packages stay where they are ──
    config/                      # (existing) servers.yaml / deployment.yaml loading (Viper)
    ssh/                         # (existing) SSH connection + remote exec
    deploy/                      # (existing) deploy + rollback logic

    # ── new control-plane glue (used by the CLI) ──
    agentclient/                 # NEW: HTTP client the CLI uses to reach the agent
                                 #      (opens SSH tunnel, calls /status, /logs)

    # ── new agent-side packages (compiled into the sail-agent binary) ──
    agent/
      supervisor/                # NEW: watch loop, restart, backoff, flap detection
      collector/                 # NEW: per-container log streaming + storage
      alerter/                   # NEW: Discord + email dispatch, dedup/throttle
      api/                       # NEW: local HTTP server (/status, /logs, /apps)
      docker/                    # NEW: thin wrapper over the Docker Engine API
      state/                     # NEW: in-memory + on-disk state of supervised apps
  cmd/sail-agent/                # NEW: main() for the agent binary
    main.go
```

Two binaries, one repo: `go build ./` → `sail` (CLI), `go build ./cmd/sail-agent`
→ `sail-agent`. Shared code (config types, the app model) lives in `internal/` and
both import it. This keeps your existing structure and adds a parallel agent tree
beside it rather than reshaping what works.

---

## 4. How the four agent subsystems fit together

```
        ┌──────────────────────────── sail-agent ────────────────────────────┐
        │                                                                     │
Docker  │   ┌────────────┐  events   ┌─────────────┐  state    ┌──────────┐  │
Engine ─┼─► │  docker/   │ ────────► │ supervisor/ │ ───────►  │ state/   │  │
API     │   │  (wrapper) │           │ restart+    │           │ apps,    │  │
        │   └────────────┘           │ backoff     │           │ history  │  │
        │         │                  └─────────────┘           └──────────┘  │
        │         │ log streams            │ transition               ▲       │
        │         ▼                        ▼                          │       │
        │   ┌────────────┐          ┌─────────────┐                   │       │
        │   │ collector/ │          │  alerter/   │ ─► Discord/email  │       │
        │   │ buffer+    │          └─────────────┘                   │       │
        │   │ store logs │                                            │       │
        │   └────────────┘                                            │       │
        │         │                                                   │       │
        │         └───────────────────┬───────────────────────────────┘       │
        │                             ▼                                        │
        │                       ┌──────────┐   HTTP (127.0.0.1)                │
        │                       │   api/   │ ◄──── sail CLI (over SSH tunnel)  │
        │                       └──────────┘                                   │
        └─────────────────────────────────────────────────────────────────────┘
```

- **`docker/`** is the only package that imports the Docker SDK. Everything else
  depends on *its* interface, so the rest of the agent is testable with a fake
  Docker. Same inversion principle you'd get from Clean Architecture, at one
  boundary that actually earns it.
- **`supervisor/`** consumes Docker events + poll results, decides restart/alert,
  writes to `state/`.
- **`alerter/`** is fed *transitions* ("app X went down"), not raw events — it
  never polls; it reacts. Keeps alert logic (dedup, throttle, routing to
  Discord/email) in one place.
- **`api/`** reads from `state/` and `collector/` to answer the CLI. It never
  contains supervision logic — it's a read/query surface.

---

## 5. State: what the agent must remember

`state/` holds, per supervised app: desired state (should be running), last
observed state, restart count in the current window, last N state transitions,
and a pointer into the log buffer. Kept in memory for speed, **persisted to a
small on-disk file** (JSON or BoltDB) so a restart of the *agent itself* doesn't
lose supervision config or forget what it was watching.

**The invariant that makes the agent real:** *if the agent process restarts, it
re-reads its persisted app list and resumes supervising every app it was
supervising before — without you re-registering anything.* An agent that forgets
its job on restart is worse than no agent.

---

## 6. Alerting model (Discord + email)

Alerts fire on **state transitions**, never on steady state:

| Transition | Alert |
|------------|-------|
| running → down | "🔴 `app` is down" |
| down → running (auto-restarted) | "🟢 `app` recovered (restart #n)" |
| N restarts within window (flapping) | "⚠️ `app` is flapping — n restarts in Xs" |
| health check failing | "🟠 `app` unhealthy" |

**Dedup + throttle live in `alerter/`:** a container flapping 50 times must not
send 50 emails. Collapse to one alert per transition-class per cooldown window.
Routing (Discord webhook, SMTP) is pluggable behind a `Notifier` interface so
adding Slack/Telegram later is a new implementation, not a rewrite.

Config extends your existing YAML style:

```yaml
# alerts.yaml  (loaded via Viper, consistent with servers.yaml/deployment.yaml)
alerts:
  discord:
    webhook_url: "https://discord.com/api/webhooks/..."
  email:
    smtp_host: "smtp.example.com"
    smtp_port: 587
    to: "you@example.com"
  cooldown: "5m"      # min gap between repeat alerts for the same app+class
  flap_threshold: 5   # restarts within flap_window that counts as flapping
  flap_window: "2m"
```

---

## 7. Why this shape, explicitly

- **Reuses Sail's SSH trust** instead of inventing agent auth — the agent's API
  is `127.0.0.1`-only and reached through the tunnel Sail already opens.
- **Two binaries, one repo** — the CLI and agent share `internal/` types but
  compile separately; you're never running a daemon on your laptop or a CLI on
  the server.
- **Package-by-component, not by-layer** — matches how deploy systems are
  actually built and keeps you close to the Docker API you're trying to learn,
  with exactly one inversion boundary (`docker/`) where it pays off.
- **Extends, doesn't rewrite** — every existing command and package stays;
  supervision is added *beside* deploy, and `deploy` gains one line: register the
  app with the agent.

---

## 8. Delivery order (see DEVELOPMENT.md for the build plan)

1. `sail-agent` skeleton + `docker/` wrapper + `api/status` — agent can list
   containers.
2. `supervisor/` — auto-restart on crash with backoff. **(kills your #1 pain)**
3. `alerter/` — Discord + email on down/recover. **(kills your alert pain)**
4. `collector/` + `sail logs` — aggregated logs. **(kills your "find where it
   breaks" pain)**
5. `sail agent install` — one-command systemd install on the VPS.

Each phase is independently useful and runs your real containers before the next
begins.

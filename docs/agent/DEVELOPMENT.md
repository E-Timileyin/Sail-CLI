# Sail — Development Plan

How to build the `sail-agent` extension on top of the existing Sail CLI. Read
`ARCHITECTURE.md` first — this assumes the two-component (CLI + agent) split.

---

## 1. Prerequisites

- **Go 1.16+** (matches Sail's current `go.mod`; bump to 1.22+ if you want newer
  stdlib — recommended)
- **Docker Engine** on the VPS (you already require this)
- The **Docker Go SDK**: `github.com/docker/docker/client` — this is the one new
  core dependency. It talks to `/var/run/docker.sock` directly, no shelling out to
  the `docker` CLI.
- Your existing stack stays: **Cobra** (commands), **Viper** (config).

```bash
cd Sail
go get github.com/docker/docker/client
```

**Test target:** a real container on your VPS you don't mind killing. Supervision
is only real when you can `docker kill` something and watch the agent bring it
back.

---

## 2. The guiding rule

**Build the agent as a separate binary from day one.** Don't try to bolt
supervision into the existing one-shot CLI — they have opposite lifecycles (CLI
exits, agent runs forever). Two `main`s, shared `internal/`.

```bash
go build -o sail ./                 # existing CLI
go build -o sail-agent ./cmd/sail-agent   # new daemon
```

Everything below builds `sail-agent` outward from the Docker connection, then
wires the CLI to talk to it last.

---

## 3. Build phases — in order, each independently useful

The order is chosen so **each phase removes a real pain you named** and runs your
actual containers before the next begins. Do not jump ahead — the supervisor is
meaningless without the Docker wrapper under it.

| Phase | Build | Kills which pain | Done when |
|-------|-------|------------------|-----------|
| 0 | Agent skeleton + `docker/` wrapper | — | agent lists your running containers |
| 1 | `api/` + `sail status` | "no visibility" | `sail status` prints live container state |
| 2 | `supervisor/` auto-restart | **manual restart, no auto-restart** | killing a container → agent restarts it |
| 3 | `alerter/` Discord + email | **no alerts** | killing a container → you get pinged |
| 4 | `collector/` + `sail logs` | **finding where it breaks** | `sail logs app` streams aggregated logs |
| 5 | `sail agent install` (systemd) | manual setup | one command installs+starts agent on VPS |

**This week = Phase 0 + Phase 1.** Get the agent talking to Docker and reporting
status. That alone gives you visibility you don't have today.

---

## 4. Phase 0 — agent skeleton + Docker wrapper

The foundation. `docker/` is the only package that imports the Docker SDK;
everything else depends on its interface so the rest of the agent is testable
without Docker.

Define the interface first — this is the seam the whole agent is built on:

```go
// internal/agent/docker/docker.go
package docker

type ContainerState struct {
    ID      string
    Name    string
    Status  string // "running", "exited", ...
    Health  string // "healthy", "unhealthy", "none"
    ExitCode int
}

// Client is the seam. supervisor/, collector/, api/ depend on THIS,
// not on the Docker SDK. Swap in a fake for tests.
type Client interface {
    List(ctx context.Context) ([]ContainerState, error)
    Restart(ctx context.Context, id string) error
    Logs(ctx context.Context, id string) (io.ReadCloser, error)
    Events(ctx context.Context) (<-chan Event, error) // die/start/health events
}
```

Then one real implementation over `github.com/docker/docker/client`. Phase 0 is
done when `sail-agent` starts, connects to the socket, and logs the list of your
running containers every few seconds.

---

## 5. Phase 1 — status API + `sail status`

Give the agent a local HTTP surface and teach the CLI to read it.

**Agent side** (`internal/agent/api/`): an HTTP server bound to `127.0.0.1:<port>`
with `GET /status` returning the current container states as JSON. Bound to
localhost only — never `0.0.0.0`. The only way in is the SSH tunnel.

**CLI side** (`internal/agentclient/` + `cmd/status.go`): open a local
port-forward over the SSH connection Sail already builds (reuse `internal/ssh/`),
`GET /status` through the tunnel, print a table.

```
$ sail status production
APP            STATE     HEALTH     UPTIME    RESTARTS
web            running   healthy    4h12m     0
worker         running   —          4h12m     0
redis          running   healthy    4h12m     0
```

Done when `sail status` shows live state of your real containers over SSH. **You
now have visibility you didn't have — first pain gone.**

---

## 6. Phase 2 — the supervisor (the core)

The heart of the project. A loop that reconciles *desired* vs *observed* state.

**Logic:**
1. Subscribe to Docker events (`die`, `health_status`) via `docker.Client.Events`.
2. On a supervised container dying / going unhealthy → restart it.
3. **Backoff:** don't hot-loop restarts. Exponential backoff (1s, 2s, 4s…capped).
4. **Flap detection:** if it restarts N times within a window, stop restarting and
   mark it *flapping* (a crash loop won't be fixed by more restarts — escalate to
   an alert instead).
5. Record every transition in `state/` with a timestamp.

```go
// sketch — internal/agent/supervisor/supervisor.go
func (s *Supervisor) onDown(app *state.App) {
    if app.FlappingWithin(s.cfg.FlapWindow, s.cfg.FlapThreshold) {
        s.events <- Transition{App: app.Name, Kind: Flapping}
        return // stop restarting a crash loop
    }
    delay := app.NextBackoff()
    time.AfterFunc(delay, func() {
        _ = s.docker.Restart(ctx, app.ID)
        app.RecordRestart()
        s.events <- Transition{App: app.Name, Kind: Restarted}
    })
}
```

**Test it for real:** `docker kill` a supervised container → agent restarts it
within seconds. `docker kill` it in a loop → agent stops after the flap threshold
and marks it flapping. **Two pains gone: auto-restart, no manual intervention.**

Write this with the fake `docker.Client` so you can unit-test backoff and flap
logic without killing real containers, then verify against a real one.

---

## 7. Phase 3 — the alerter (Discord + email)

`supervisor/` already emits `Transition` events. `alerter/` consumes them and
notifies. It never touches Docker — it reacts to transitions only.

**`Notifier` interface** so Discord and email are two implementations, and Slack/
Telegram later is a third without touching the core:

```go
type Notifier interface {
    Notify(ctx context.Context, alert Alert) error
}
// discordNotifier: POST to webhook. emailNotifier: SMTP send.
```

**Dedup + throttle (the part that matters):** a flapping container must not send
50 emails. Keep a per-app-per-alert-class cooldown; collapse repeats within the
window into one. This is the difference between a useful alerter and a spam
machine — build it in from the start, not as an afterthought.

Alerts fire on: down, recovered, flapping, unhealthy (per ARCHITECTURE §6). Config
in `alerts.yaml` via Viper, consistent with your existing config files.

Done when killing a container pings your Discord and emails you — once, not fifty
times. **Alert pain gone.**

---

## 8. Phase 4 — log aggregation + `sail logs`

**Agent side** (`collector/`): for each supervised container, stream
stdout/stderr via `docker.Client.Logs`, tag lines with the app name and
timestamp, keep a bounded in-memory ring buffer (last N MB) plus optional
append to a per-app file on disk. Bounded — a log collector that OOMs the VPS is
worse than no collector.

**CLI side** (`cmd/logs.go`): `sail logs <app> [--follow]` streams from the agent
over the SSH tunnel.

```
$ sail logs web --follow
web  10:22:01  GET /health 200
web  10:22:03  GET /api/users 500  ← error visible without SSHing in
```

Done when you can find where something broke without SSHing in and running
`docker logs` per container. **"Finding where it breaks" pain gone.**

---

## 9. Phase 5 — one-command install

`sail agent install <server>`: SSH to the VPS (reuse `internal/ssh/`), copy the
`sail-agent` binary, write a **systemd unit** so the agent starts on boot and
restarts if it dies (systemd supervises the supervisor), start it.

```ini
# /etc/systemd/system/sail-agent.service
[Unit]
Description=Sail Agent
After=docker.service
Requires=docker.service

[Service]
ExecStart=/usr/local/bin/sail-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Done when `sail agent install production` leaves a running, boot-persistent agent
on the VPS with zero manual steps.

---

## 10. Testing strategy

```bash
go test ./...                          # everything
go test ./internal/agent/supervisor/   # backoff + flap logic (fake docker.Client)
go test ./internal/agent/alerter/      # dedup + throttle (fake Notifier)
go test -race ./internal/agent/...     # agent is concurrent — race-check it
```

**Unit-test the logic that's hard to trigger live:** backoff timing, flap
detection, alert dedup. All of these run against the fake `docker.Client` /
`Notifier` — that's the payoff of defining those interfaces in Phase 0/3.

**Integration-test against real Docker** for the happy path: start a throwaway
container, `docker kill` it, assert the agent restarted it. Gate these behind a
build tag so `go test ./...` stays fast and doesn't need Docker.

---

## 11. The invariants that define "done"

Per phase, the thing that must be true:

- [ ] **Phase 1:** `sail status` reflects real container state over SSH
- [ ] **Phase 2:** `docker kill`'d container comes back within seconds; crash-loop
      is detected and restart stops (flapping)
- [ ] **Phase 3:** down → one alert to Discord + email; flapping → one alert, not
      fifty
- [ ] **Phase 4:** `sail logs app` shows aggregated logs; collector memory is
      bounded
- [ ] **Phase 5:** agent survives a **reboot of the VPS** and resumes supervising
      every app it had registered — no re-registration

That last one is the real test of the whole system: **the agent forgets nothing
across its own restart.** Persist supervised-app state to disk (Phase 2 onward)
and reload it on startup.

---

## 12. Git workflow

Branch per phase off `main`, commit per meaningful step, merge when the phase's
invariant passes:

```bash
git checkout -b agent/phase-0-docker-wrapper
# ... build, test ...
git commit -m "agent: docker client wrapper + container listing"
```

Keep the existing CLI on `main` working at every merge — you're extending a tool
you actually use, so `main` should always deploy. Never merge a phase that breaks
`sail deploy`.

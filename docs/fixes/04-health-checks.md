# Fix 04 — Real health checks

**Effort:** Medium · **Risk:** Low–Medium · **Depends on:** Fix 03 (for full value)

## Problem

`Orchestrator.verifyDeployment` (`internal/workflows/orchestrator.go`) currently does:

```go
time.Sleep(5 * time.Second)
status, _ := o.dockerMgr.ContainerStatus(ctx, config.ContainerName)
if status != "running" { return fmt.Errorf(...) }
```

"Running" is not "healthy." A container can be up while the app inside is crash-looping,
returning 500s, or still starting. The SSH path is even shallower — it only runs
`docker ps`.

## Why it matters

Health checks are what make rollback **meaningful**. Rollback fires on a failure signal; if
that signal is a shallow `docker ps`, a broken-but-currently-up container passes
verification, the backup is deleted, and now there is **no rollback target**. The quality of
the health check directly bounds the quality of the safety guarantee.

## Proposed change

Replace the single sleep with a **retry-until-deadline** probe, driven by the
already-defined `model.ContainerHealthCheck` fields (currently unused).

```go
func (o *Orchestrator) verifyDeployment(ctx context.Context, cfg *model.Deployment) error {
    hc := cfg.HealthCheck
    if hc == nil {
        return o.waitRunning(ctx, cfg.ContainerName)   // fallback: current behavior
    }

    interval := parseDur(hc.Interval, 2*time.Second)
    timeout  := parseDur(hc.Timeout, 30*time.Second)
    deadline := time.Now().Add(timeout)

    for attempt := 1; time.Now().Before(deadline); attempt++ {
        if err := o.probe(ctx, cfg, hc); err == nil {
            o.logger.Infof("healthy after %d attempt(s)", attempt)
            return nil
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(interval):
        }
    }
    return fmt.Errorf("health check did not pass within %s", timeout)
}
```

`probe` runs the configured check — either an HTTP `GET` against a mapped port, or the
container's `test` command via `docker exec`:

```yaml
# deployment.yaml
health_check:
  test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
  interval: 2s
  timeout: 30s
  retries: 5
```

On failure the existing rollback path in `Deploy()` fires automatically — no change needed
there, only the signal improves.

## Design notes

- **Poll, don't sleep.** A fixed sleep either wastes time (too long) or false-fails a
  slow-starting app (too short). A deadline with retries tolerates variable startup while
  still failing decisively.
- **Respect the container's own `HEALTHCHECK`.** If the image defines one, prefer reading
  Docker's reported health state over re-implementing it.
- **Distinguish "starting" from "unhealthy"** using `hc.StartPeriod` so early failures
  during warm-up don't trigger a premature rollback.

## Acceptance criteria

- [ ] `verifyDeployment` polls until healthy or a configurable deadline, not a fixed sleep.
- [ ] A container that is "running" but failing its health probe is treated as a failed deploy.
- [ ] A failed health check triggers rollback (backup preserved, prior version restored).
- [ ] `interval`, `timeout`, `retries`, and `start_period` from config are honored.
- [ ] No health-check config falls back to today's behavior (no breaking change).

# Fix 03 — Unify on one Docker daemon & wire the Orchestrator into the CLI

> **⚠️ SUPERSEDED — do not implement as written.** The core proposal below (point the
> Docker Go SDK at `ssh://user@host:port` via `client.WithHost`) **does not work**.
> `client.WithHost` only overrides a host string; the `ssh://` scheme is interpreted by
> `cli/connhelper/ssh` in `github.com/docker/cli`, which this project does not depend on.
> See [`docs/adr/0001-target-architecture.md`](../adr/0001-target-architecture.md) for the
> verification and for the architecture that replaced this one: drive `docker compose` on
> the target through a forced-command `sail agent`.
>
> The *diagnosis* below remains correct and still matters: Sail has two disconnected
> deploy implementations, and the Orchestrator's real rollback logic never reaches
> production. Only the prescribed solution is withdrawn.

**Effort:** Large · **Risk:** Medium (structural) · **Depends on:** benefits from 02

## Problem

Sail has two disconnected deploy implementations:

- **`cmd/deploy.go`** runs `docker-compose` **on the remote server** via SSH shell commands.
  This is what the CLI actually uses.
- **`internal/workflows/Orchestrator`** implements real backup/rollback but talks to a
  Docker daemon through the **local** SDK (`client.FromEnv` in `internal/docker/manager.go`)
  — i.e. the operator's laptop, not the server. It is **not wired into any command**.

You can't simply call the Orchestrator from `runDeploy`: it would deploy to the wrong
machine. The two paths must be reconciled first.

## Why it matters

The Orchestrator is the single most valuable piece of logic in the codebase — the
rename-to-backup → verify → promote/restore saga that gives you rollback-safe deploys.
Today none of that reaches production. Wiring it in is what makes Sail trustworthy for real
deployments rather than just a nicer way to run `docker-compose`.

## Key insight

The Docker SDK can talk to **any** daemon via the host setting, including
**`ssh://user@host`**. Point the SDK-based Manager at the remote server's daemon over SSH,
and the Orchestrator's logic executes **against production** — no need to reimplement it as
brittle remote shell strings.

## Proposed change

### 1. Teach `Manager` to target a specific server

```go
// internal/docker/manager.go
func NewManagerForServer(logger *logrus.Logger, s *model.ServerStruct) (*Manager, error) {
    host := fmt.Sprintf("ssh://%s@%s:%d", s.User, s.Host, s.Port)
    cli, err := client.NewClientWithOpts(
        client.WithHost(host),
        client.WithAPIVersionNegotiation(),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create Docker client for %s: %w", s.Name, err)
    }
    if _, err := cli.Ping(context.Background()); err != nil {
        return nil, fmt.Errorf("cannot reach Docker on %s: %w", s.Name, err)
    }
    return &Manager{client: cli, logger: logger}, nil
}
```

Keep the existing `NewManager` (local, `FromEnv`) for local use and tests.

### 2. Rewrite `runDeploy` to use the Orchestrator

```go
func runDeploy(cmd *cobra.Command, args []string) error {
    servers, err := config.LoadConfig(args[0])
    if err != nil { return fmt.Errorf("load config: %w", err) }
    deployCfg, err := config.LoadDeployment(deploymentFile)   // deployment.yaml → model.Deployment
    if err != nil { return fmt.Errorf("load deployment: %w", err) }

    var failed []string
    for i := range servers {
        s := &servers[i]
        mgr, err := docker.NewManagerForServer(logger.Log, s)
        if err != nil { logger.Log.Error(err); failed = append(failed, s.Name); continue }

        orch := workflows.NewOrchestrator(logger.Log, mgr)
        if dryRun {
            logger.Log.Infof("[dry-run] would deploy %s:%s to %s", deployCfg.Image, deployCfg.Tag, s.Name)
            continue
        }
        if err := orch.Deploy(cmd.Context(), deployCfg); err != nil {
            logger.Log.Errorf("deploy failed on %s: %v", s.Name, err)
            failed = append(failed, s.Name)
        }
    }
    if len(failed) > 0 {
        return fmt.Errorf("deploy failed on: %s", strings.Join(failed, ", "))
    }
    return nil
}
```

The SSH+compose logic in `executeDeployment` / `runCommand` is retired (or kept behind a
`--legacy-compose` flag during transition).

### 3. Prefer the `internal/container/` client

Longer term, migrate the Orchestrator off the concrete `internal/docker/Manager` and onto
the interface-based `internal/container/service` (the target architecture). The
`types.ContainerManager` interface already sketches the unified API
(`Deploy`/`Rollback`/`GetStatus`/`GetLogs`) this should converge on.

## Caveats

- **Auth:** the `ssh://` Docker transport uses your SSH agent / `~/.ssh` config and does
  **not** support SSH password auth. This nudges toward key-based auth (which Fix 02 and
  Fix 05 also want). Password-only servers won't work with this path.
- **Remote Docker over SSH** requires the Docker CLI/daemon reachable for the SSH user
  (docker group membership).
- **`--skip-backup`** becomes meaningful here — thread it into the Orchestrator to skip the
  rename-to-backup step.

## Acceptance criteria

- [ ] `sail deploy` performs a real deploy against the **remote** server's Docker daemon.
- [ ] A failed start or failed verification triggers automatic rollback to the prior container.
- [ ] The Orchestrator is reachable from the CLI (no more dead code).
- [ ] `--dry-run` and `--skip-backup` flow through to the Orchestrator.
- [ ] Multi-server deploys still fail-soft with a correct aggregate exit code (Fix 01).

# Fix 01 — Exit codes & wire up the deploy flags

**Effort:** Small · **Risk:** Low · **Depends on:** nothing

## Problem

Two independent correctness bugs in `cmd/deploy.go`:

1. **`runDeploy` always returns `nil`.** The per-server loop logs failures and `continue`s,
   but the function never returns an error. A deploy that fails on every server still exits
   with code `0`. Any CI job or script wrapping `sail deploy` will believe it succeeded.

2. **`--dry-run` and `--skip-backup` are declared but never read.** They're registered as
   flags and bound to package vars (`dryRun`, `skipBackup`), but nothing in `runDeploy`
   references them. They are silent no-ops — a user passing `--dry-run` gets a real deploy.

## Why it matters

A deployment tool's single most important contract is telling the truth about what
happened. A flag that silently does nothing, or a failure that reports success, erodes all
trust in the tool and can cause real outages (e.g. CI marks a broken deploy green).

## Proposed change

### 1. Return a non-zero result on any failure

Track failed servers and return an aggregated error:

```go
func runDeploy(cmd *cobra.Command, args []string) error {
    servers, err := config.LoadConfig(args[0])
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }

    var failed []string
    for i := range servers {
        s := &servers[i]
        if err := deployToServer(cmd.Context(), s); err != nil {
            logger.Log.Errorf("deploy failed on %s: %v", s.Name, err)
            failed = append(failed, s.Name)
        }
    }

    if len(failed) > 0 {
        return fmt.Errorf("deploy failed on %d/%d servers: %s",
            len(failed), len(servers), strings.Join(failed, ", "))
    }
    return nil
}
```

`cmd.Execute()` already calls `os.Exit(1)` when a command returns an error
(`cmd/root.go`), so this is all that's needed for a correct exit code.

### 2. Make `--dry-run` do something

Short-circuit before any mutating command and print the plan:

```go
if dryRun {
    logger.Log.Infof("[dry-run] would deploy to %s (%s): pull → down → up -d",
        s.Name, s.Address())
    return nil
}
```

### 3. Make `--skip-backup` do something (or remove it)

Today the SSH path doesn't create a backup at all, so `--skip-backup` has nothing to skip.
Two honest options:

- **Preferred:** defer this flag until Fix 03 lands (the Orchestrator is what creates
  backups), and remove the flag now so it isn't misleading.
- **Interim:** keep it but document that it only takes effect once the Orchestrator path is
  wired in.

## Acceptance criteria

- [x] `sail deploy` on an all-failing config exits non-zero.
      Verified: exits `1` with `deploy failed on 1/1 servers: <name>`.
- [x] `sail deploy` succeeding on all servers exits `0`.
- [x] `--dry-run` performs no mutating commands and logs the intended plan.
      Verified: exits `0`, logs the plan, opens no connection.
- [x] `--skip-backup` either has a real effect or is removed (no silent no-op).
      Removed, along with `--force-rebuild`.
- [x] Message states how many servers failed and which ones.

**Implemented.** Note the message counts failures against the total, so a partial failure
is distinguishable from a total one.

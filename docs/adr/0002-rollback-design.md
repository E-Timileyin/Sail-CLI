# ADR 0002 — Rollback design: tag switching, not container renaming

- **Status:** Accepted (design), implementation pending
- **Date:** 2025-11-23
- **Depends on:** [ADR 0001](0001-target-architecture.md)

## Context

`internal/workflows/orchestrator.go` contained Sail's only working rollback logic: a
lightweight saga that backed up the running container by renaming it, deployed the new
one, verified it, and restored the backup on failure. That code is being deleted as part
of the ADR 0001 restructure, because it is built on the Docker Engine SDK's container API
and ADR 0001 moves Sail onto `docker compose` (the `ssh://` SDK transport it would have
needed does not work — see ADR 0001 §Verification).

Deleting it loses working code. This ADR records the reasoning that was worth keeping, so
it does not have to be re-derived, and states what replaces it. **Nothing here is
implemented yet** — this is a design, and it should be treated as such until the
acceptance criteria are met.

## What was valuable in the old design

Not the SDK calls. The **state machine**, which is protocol-independent:

```
validate → check environment → fetch artifact → back up current
        → install new → verify healthy ─┬─ ok   → promote (drop backup)
                                        └─ fail → roll back (restore backup)
```

Three properties worth preserving:

1. **Compensate, don't retry.** A failed deploy restores the previous state rather than
   attempting to fix the new one.
2. **The backup is only discarded after verification passes.** Until then the old
   artifact remains recoverable. This is the property that makes rollback trustworthy —
   and the reason a shallow health check is dangerous: it discards the backup based on a
   signal that does not mean "working".
3. **Failure to roll back is a distinct, louder error** than failure to deploy. A deploy
   that fails and rolls back cleanly is an incident; one that fails *and* cannot roll back
   is an outage.

## What replaces it

### Rollback is a tag switch

With compose, the "current artifact" is a tag recorded on the server, not a second
container kept alive under a temp name. Sail keeps, per app, on the server:

```
apps/<name>/.tag          # tag currently deployed
apps/<name>/.tag.prev     # previous tag, written when .tag changes
apps/<name>/.env          # secrets, chmod 600, never in the repo
apps/<name>/compose.yaml
apps/<name>/deploy.log    # append-only history
```

Deploy:

1. Read `apps/<name>/.tag` as `prev`. If the target tag equals `prev`, fail early —
   redeploying the same thing is a no-op, not an incident.
2. Write the target tag into `.env` (or a generated override file) so compose resolves
   `image:tag` without the compose file itself being rewritten.
3. Run `docker compose pull && docker compose up -d --wait`.
   `--wait` blocks on the container's own `HEALTHCHECK` and exits non-zero if it never
   becomes healthy, so Sail does not need its own probe. This is the health signal the
   old `verifyDeployment` (a 5-second sleep, then "is it running?") lacked.
4. On success: `prev → .tag.prev`, `target → .tag`, append to `deploy.log`, exit 0.
5. On failure: restore the previous tag, `up -d --wait` again, append the failure and the
   rollback outcome to `deploy.log`, exit non-zero.

Rollback (`sail rollback <app>`): read `.tag.prev`, run steps 2–4 with it as the target.
Swapping `.tag` and `.tag.prev` makes rollback repeatable — rolling back twice returns to
where you started, which is what an operator expects under pressure.

### Why tag switching over container renaming

- The rename trick synthesized atomicity the SDK did not provide. Compose gives
  `up -d --wait` a real health gate, so the synthesis is unnecessary.
- One compose project owns its containers. Keeping a `-backup` container alive means two
  processes bound to the same host port, which fails, or a stopped container holding the
  old image — neither is a clean backup.
- The previous tag is a single small file, so the "backup" survives a reboot, an agent
  restart, or a failed deploy mid-way. An in-memory or container-based backup does not.

### What is lost

- **Container-level state outside the image.** If the old container had a writable layer
  with data not in a volume, renaming preserved it and a tag switch does not. This is
  already a bug in any compose deployment, not a regression, but it is a real difference
  and worth stating.
- **Instant rollback with no image pull.** The old container's image was already local.
  Rolling back to a tag whose image has been pruned requires a pull, so rollback latency
  depends on the registry. Mitigation: do not prune images referenced by `.tag` or
  `.tag.prev`.

## Acceptance criteria (not yet met)

- [ ] A failed `up -d --wait` restores the previous tag and returns the old version to
      healthy, automatically.
- [ ] `.tag.prev` is written only after verification passes.
- [ ] The previous tag is preserved across an agent restart and a mid-deploy failure.
- [ ] `sail rollback` is repeatable: rolling back twice returns to the original state.
- [ ] A deploy of the currently deployed tag fails early with a clear message.
- [ ] Rollback failure is reported distinctly from deploy failure.
- [ ] `deploy.log` records both the failed deploy and the rollback outcome.

## Open question

Whether `.tag`/`.tag.prev` should be authoritative, or whether the recorded state should
be re-derived from `docker compose ps` on each run. Files are simpler and survive agent
restarts; the compose state is the truth if someone edits the server by hand. Leaning
towards files as authoritative with a reconciliation warning when compose disagrees.

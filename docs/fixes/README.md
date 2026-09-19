# Sail — Fixes & Hardening Roadmap

These write-ups describe the changes that move Sail from "great for side projects" to
"trustworthy for real production." Each file is self-contained: problem, why it matters,
proposed change (with code sketches), and acceptance criteria.

## Priority order

| #  | Fix                                                             | Why this order                                            | Effort |
|----|-----------------------------------------------------------------|-----------------------------------------------------------|--------|
| 01 | [Exit codes & wire up flags](01-exit-codes-and-flags.md)        | Tiny change; stops failed deploys from exiting `0`        | S      |
| 02 | [Host-key verification](02-host-key-verification.md)            | Closes a MITM hole; self-contained, no arch change        | S      |
| 03 | [Unify on one Docker daemon](03-unify-docker-daemon.md)         | **⚠️ Superseded** — `ssh://` SDK proposal doesn't work; see [ADR 0001](../adr/0001-target-architecture.md) | L |
| 04 | [Real health checks](04-health-checks.md)                       | Makes the rollback trigger trustworthy                    | M      |
| 05 | Secrets handling (no write-up yet; covered by ADR 0001 §Consequences) | Removes plaintext passwords from config/git        | M      |

Adopted target architecture: [`docs/adr/0001-target-architecture.md`](../adr/0001-target-architecture.md).

**Suggested sequencing:** ship **01 + 02** together first (small, independent safety wins,
no architectural risk). Then tackle **03** a its own focused change, with **04** riding on
top of it. **05** can land any time.

## Guiding principle

The value of each fix is not a single feature — it's making the *safe* path the *default*
path, so the risky manual approach stops being tempting. Rollback logic is only as good as
the health signal that triggers it, and a deploy tool that lies about success (exit `0` on
failure) is worse than no tool. These fixes target exactly those trust boundaries.

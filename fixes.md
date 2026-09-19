## Fix these in Sail before it touches a real server

1. **`ssh.InsecureIgnoreHostKey()`.** Your own roadmap lists this. It allows MITM against every deploy. Use `knownhosts.New()` with trust-on-first-use, pinning the key in `servers.yaml`. This is the first change.
2. **Secrets in `deployment.yaml`.** The README example puts `API_KEY` in plaintext in a file that lives in the repo. In the new model the config holds no secrets. Secrets live in `apps/<name>/.env` on the server (chmod 600), and Sail only references them.
3. **`tag: latest` default.** Require an explicit tag, and make CI pass the git SHA. Sail records the previous tag on the server, and `sail rollback` redeploys it.
4. **`--force-rebuild`.** Building on the VPS is what you're avoiding. If the flag builds on the target, remove it. Builds happen locally or in CI, and the server only pulls.
5. **README install path.** It says `go install github.com/E-Timileyin/sail@latest` and `git clone .../Sail.git`, but the repo is `Sail-CLI`. `go install` resolves from the module path in `go.mod`, so a mismatch breaks it. Check `go.mod` and fix one side.

## What to change in the design

- **Drive `docker compose`, not `docker run`.** Your `deployment.yaml` (image, ports, env, restartPolicy) can't express healthchecks, memory limits, per-project networks, or a DB container. Shrink the config to roughly `app`, `image`, `server`, `domain`, `port`, and let the compose file on the server carry the rest.
- **Don't build health checks yourself.** `docker compose up -d --wait` already blocks on the container's healthcheck. Sail runs it and rolls back on failure. That closes your "Enhanced Health Checks" item in about ten lines.
- **One binary, two roles.** A forced-command SSH key allows exactly one command, but Sail currently opens a session and runs several (`docker --version`, and so on), so the two conflict. Fix: `sail agent deploy <app> <sha>` runs server-side from the same binary. `authorized_keys` pins the forced command to `sail agent`, and the client just calls `ssh host "deploy app sha"`. A leaked CI key can then only redeploy an existing app's tag.

## Subcommands to add

| Command                            | Replaces                                                              |
| ---------------------------------- | --------------------------------------------------------------------- |
| `sail bootstrap <server>`          | `bootstrap.sh` (harden, docker, networks, caddy)                      |
| `sail app new <name> <domain>`     | `bin/new-app` (scaffold, secrets, Caddy site, reload)                 |
| `sail app rm <name>`               | `bin/remove-app`                                                      |
| `sail deploy <app> --tag <sha>`    | `bin/deploy`                                                          |
| `sail rollback <app>`              | swap `.tag` to previous, `up -d --wait`                               |
| `sail logs`, `sail history`        | your "Deployment History" item, from an append-only log on the server |
| `sail init --stack go\|node\|next` | copies Dockerfile, `.dockerignore`, and CI workflow into a project    |

The reusable GitHub Actions workflow then downloads a released Sail binary and runs `sail deploy`. Set up GoReleaser so CI installs from a Release instead of `go install`.

## Sequencing

Don't block your projects on the rewrite. `bin/deploy` is 15 lines of bash and the model is the same. Run the bash version on the VPS now, ship real apps with it, and port each piece into Sail as it proves out. Doing it in that order means you build Sail against a design that has already survived production. Sail is worth the work because it's Go platform tooling you can show, but only if it ends up something you use yourself.

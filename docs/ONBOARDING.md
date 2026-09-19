# Onboarding

Getting Sail running, and finding your way around the code.

Read [ARCHITECTURE.md](ARCHITECTURE.md) for how it fits together, and the
[ADRs](adr/) for why it was built this way.

---

## 1. Build and run

```bash
git clone https://github.com/E-Timileyin/Sail-CLI.git
cd Sail-CLI
go build -o sail .
./sail --help
```

`go install ...@latest` does **not** work yet: `go.mod` declares
`github.com/E-Timileyin/sail` while the repo is `E-Timileyin/Sail-CLI`, and `go install`
resolves via the module path. Tracked as an issue.

## 2. Config

Create `config.yaml`. It requires `version: 2` and holds no secrets:

```yaml
version: 2
app:
  name: Sail
  environment: development
servers:
  - name: production
    host: your-server-ip
    user: deploy
    key_path: ~/.ssh/id_ed25519
```

A v0.1.0-style config is rejected with guidance rather than reinterpreted. `password:` is
gone; password auth is not supported, because Sail verifies host keys and a config file
should not carry a credential.

## 3. Commands

```bash
./sail app new my-api --image ghcr.io/you/my-api --port 8080   # scaffold on the server
./sail deploy my-api --tag $(git rev-parse --short HEAD)       # deploy a tag
./sail status my-api                                            # recorded tag + container state
./sail rollback my-api                                          # back to the previous tag
./sail logs my-api --tail 100
./sail history my-api
./sail app rm my-api --yes                                      # deletes its .env
```

`--dry-run` prints the plan without connecting, so it works on a machine with no key and
no config. `--accept-new` records an unknown host key on first connect.

## 4. Secrets

`app new` creates `<srv-root>/<app>/.env` empty, mode 600, and **never overwrites an
existing one**. Put the app's secrets in it yourself. Sail's config holds no secrets, and
nothing in the repo should.

## 5. Code tour

| You want to change… | Start in… |
|---|---|
| A CLI flag or command | `cmd/` |
| What a deploy does, step by step | `internal/remote/deployer.go` |
| The remote commands themselves | `internal/remote/plan.go` |
| The generated compose file | `internal/remote/templates.go` |
| Host-key or auth policy | `internal/sshx/` |
| Config parsing and validation | `internal/config/loader.go` |
| Types shared across packages | `internal/domain/types.go` |

Two rules worth knowing before you edit:

1. **Never build an `ssh.ClientConfig` outside `sshx`, and never dial outside
   `internal/remote.Runner`.** Host-key verification duplicated across call sites is how
   `InsecureIgnoreHostKey()` survived in two places. There is a test that fails the build
   if it returns.
2. **Every config struct needs both `yaml` and `mapstructure` tags.** Viper reads
   `mapstructure`; with yaml-only tags, multi-word keys silently unmarshal empty.

## 6. Tests

```bash
go test ./... -race           # everything
go test ./internal/remote/    # one package
go test ./internal/sshx/ -run TestHandshake -v
```

No test needs a server. SSH tests start an in-process server; the deploy state machine
uses a fake executor.

## 7. Before opening a PR

```bash
gofmt -l .        # must be empty
go vet ./...
go test ./... -race
```

CI runs these plus `go mod tidy` verification and `govulncheck`. See
[CONTRIBUTING.md](../CONTRIBUTING.md).

## 8. Where to start

Open issues are the source of truth for what needs doing. Good first issues are labelled.
The highest-value work right now:

- **Verify the health gate against a real daemon.** Everything depends on
  `docker compose up -d --wait` exiting non-zero for an unhealthy container, and that is
  only asserted against a fake.
- **`sail init`** — scaffold a Dockerfile, `.dockerignore` and a CI workflow locally.
- **GoReleaser** — so releases have binaries and install does not need a Go toolchain.

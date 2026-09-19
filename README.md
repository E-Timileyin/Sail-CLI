---

# ⚓ Sail — Automated Docker Deployment CLI

A fast, secure, and flexible CLI tool for **deploying Dockerized applications to remote servers via SSH**.
Built with **Go**, **Cobra**, and **Viper**, `Sail` automates container updates, rollbacks, and monitoring.

---

## 🚀 Quick Start

### Prerequisites

- Go 1.16+
- Docker and Docker Compose installed on target servers
- SSH access to target servers
- A Dockerized application with a `docker-compose.yml` file

## 📦 Installation

There are two ways to install and use Sail.

> **⚠️ Install note.** The v0.1.0 release shipped **no binary artifacts**, and the
> `go install github.com/E-Timileyin/sail@latest` command it advertised **does not
> resolve**: `go install` fetches from the module path declared in `go.mod`, which is
> `github.com/E-Timileyin/sail`, and no repository exists at that path (the repo is
> `E-Timileyin/Sail-CLI`). Until the module path is corrected and a Release with assets
> is published, **install from source**.

### Option 1: From Source (works today)

```bash
git clone https://github.com/E-Timileyin/Sail-CLI.git
cd Sail-CLI
go build -o sail .
./sail --help
```

### Option 2: From a Release

Not available yet: v0.1.0 has no attached binaries and there is no GoReleaser config.
Once a release with assets exists, `go install github.com/E-Timileyin/Sail-CLI@latest`
will work provided the module path matches the repository.

3.  **Run the executable:**
    ```bash
    ./sail --help
    ```

3.  **(Optional) Move it to your PATH:**
    ```bash
    sudo mv sail /usr/local/bin/
    ```

## 🚀 Usage

### 1. Configuration

Create a `config.yaml`. It requires `version: 2` and holds **no secrets** —
application secrets live in `apps/<name>/.env` on the server (chmod 600).

```yaml
# config.yaml
version: 2

app:
  name: Sail
  environment: production

servers:
  - name: production
    host: your-server-ip
    port: 22
    user: deploy
    key_path: ~/.ssh/id_ed25519
```

Sail verifies host keys against `~/.ssh/known_hosts`. On first connect to a new host,
pass `--accept-new`, which records the key (the same behaviour as OpenSSH's
`StrictHostKeyChecking=accept-new`). A host whose key has changed is always refused.

Password authentication is not supported.

### 2. Basic Commands

```bash
# Deploy, showing the plan without changing anything
./sail deploy config.yaml --dry-run

# First deploy to a new server: record its host key, then deploy
./sail deploy config.yaml --accept-new

# SSH into the configured server
./sail ssh production

# Show version information
./sail --version
```

### 3. Advanced Deployment Options

```bash
# Dry run: print the plan, connect to nothing, change nothing
./sail deploy config.yaml --dry-run

# Trust a new host on first connect and record its key in known_hosts
./sail deploy config.yaml --accept-new

# Use an alternate known_hosts file
./sail deploy config.yaml --known-hosts /path/to/known_hosts
```

`--skip-backup` and `--force-rebuild` were removed. `--skip-backup` never did anything
(the SSH path created no backup), and `--force-rebuild` implied building on the target
server, which defeats the purpose of building locally or in CI.

## 🧪 Testing

To run the project's test suite, execute the following command from the root directory:

```bash
go test -v ./...
```

This will run all unit and integration tests and provide detailed output.

## 🔧 Features

- **Automated Deployments**: Deploy your Docker containers with a single command.
- **Host-key verification**: SSH host keys are checked against `known_hosts`; a changed
  key is refused rather than trusted.
- **Honest exit codes**: a failed deploy exits non-zero, so CI cannot mark it green.
- **Dry run**: `--dry-run` prints the plan and changes nothing.
- **Multi-server**: deploys to every configured server, reports which failed.
- **Key-only SSH**: no plaintext passwords in config, ever.

### Upcoming Features

These are **not implemented yet**. They are listed so the gap is visible, not claimed.

- **Automatic Rollbacks**: reverts to the previous tag when a deploy fails. The
  `internal/workflows` machinery exists but is unreachable from the CLI — see
  `docs/adr/0001-target-architecture.md`.
- **Real health checks**: gating on the container's own `HEALTHCHECK` via
  `docker compose up -d --wait`.
- **Deployment History**: `sail history`, from an append-only log on the server.
- **Server-side agent**: `sail agent` as a forced-command SSH key, so a leaked CI key
  cannot obtain a shell.
- **Scaffolding**: `sail bootstrap`, `sail app new`, `sail init --stack go|node|next`.
- **Pre/Post Deployment Hooks**.

See `docs/fixes/README.md` for status and `fixes.md` for the roadmap.

## 🔒 Security Best Practices

1. **Use SSH Keys**: key authentication only; passwords are not supported.
2. **Verify host keys on first connect**: use `--accept-new` deliberately, then review
   the entry it writes to `~/.ssh/known_hosts`.
3. **Keep secrets off the repo**: application secrets belong in `apps/<name>/.env` on the
   server (chmod 600), never in `config.yaml`.
4. **Least Privilege**: use a dedicated deployment user with minimal permissions.
5. **Firewall**: expose only the ports you need.
6. **Regular Updates**: keep Docker and system packages current.

## 🐛 Troubleshooting

### Common Issues

#### 1. Docker not installed on target server
```
Error: docker is not installed on the server: command failed: docker --version
```
**Solution**: Install Docker on the target server:
```bash
# For Ubuntu/Debian
sudo apt-get update
sudo apt-get install docker.io docker-compose
sudo systemctl enable --now docker

# Add user to docker group
sudo usermod -aG docker $USER
newgrp docker  # Apply group changes
```

#### 2. Permission denied (publickey)
```
Failed to connect: ssh: handshake failed: ssh: unable to authenticate...
```
**Solution**:
- Verify your SSH credentials
- Ensure the private key has correct permissions:
  ```bash
  chmod 600 ~/.ssh/id_rsa
  ```
- Verify your SSH key is loaded and the path in `key_path` is correct.
- Password authentication is not supported; use a key.

## 🤝 Contributing

Contributions are welcome! Please read our [contributing guidelines](CONTRIBUTING.md) for details.

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
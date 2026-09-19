# Fix 02 — Host-key verification

**Effort:** Small · **Risk:** Low · **Depends on:** nothing

## Problem

`ServerStruct.SSHConfig()` (`internal/model/server.go`) sets:

```go
HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: Implement proper host key verification
```

This accepts **any** host key without verification. A machine-in-the-middle can impersonate
your production server; Sail will connect and hand over whatever the deploy session carries
(commands, and — with password auth — the password itself).

## Why it matters

Host-key verification is the mechanism that proves you're talking to the server you think
you are. Disabling it removes the authentication half of SSH's security model. For a tool
whose whole job is connecting to production servers, this is the highest-value security fix.

## Proposed change

Verify against the operator's `~/.ssh/known_hosts` using `golang.org/x/crypto/ssh/knownhosts`
(already available — the project depends on `golang.org/x/crypto`).

```go
import (
    "path/filepath"
    "golang.org/x/crypto/ssh"
    "golang.org/x/crypto/ssh/knownhosts"
)

func hostKeyCallback() (ssh.HostKeyCallback, error) {
    home, err := os.UserHomeDir()
    if err != nil {
        return nil, fmt.Errorf("cannot resolve home dir: %w", err)
    }
    khPath := filepath.Join(home, ".ssh", "known_hosts")
    cb, err := knownhosts.New(khPath)
    if err != nil {
        return nil, fmt.Errorf("cannot load known_hosts (%s): %w", khPath, err)
    }
    return cb, nil
}
```

Then in `SSHConfig()`:

```go
cb, err := hostKeyCallback()
if err != nil {
    return nil, err
}
return &ssh.ClientConfig{
    User:            s.User,
    Auth:            authMethods,
    HostKeyCallback: cb,            // was: ssh.InsecureIgnoreHostKey()
    Timeout:         30 * time.Second,
}, nil
```

### Trust-on-first-use (TOFU) for new hosts

To keep first-connect ergonomics without the blanket "trust everyone" hole, add an opt-in
`--accept-new` flag mirroring OpenSSH's `StrictHostKeyChecking=accept-new`: when set and the
host is unknown, append its key to `known_hosts`, then verify normally on subsequent runs.
Wrap the base callback so an unknown-key error triggers the append-and-retry.

## Notes / interaction with Fix 03

If Fix 03 moves the Docker connection onto the `ssh://` transport, host-key verification for
that path is handled by your `~/.ssh/config` and `known_hosts` automatically — so this fix
and Fix 03 reinforce each other. This fix still stands on its own for the current SSH path.

## Acceptance criteria

- [x] `ssh.InsecureIgnoreHostKey()` is removed.
      Both call sites. Guarded by a test that fails the build on reintroduction.
- [x] Connecting to a host with a mismatched key fails with a clear error.
      Verified against a real SSH handshake in `internal/sshx/handshake_test.go`; the
      test fails if the insecure callback is reinstated.
- [x] Connecting to a known, matching host succeeds.
- [x] `--accept-new` (or equivalent) provides a documented path for first-time hosts.
- [x] A missing/unreadable `known_hosts` produces an actionable error message.

**Implemented.** One difference from the sketch below: the policy lives in
`internal/sshx`, not in `model.ServerStruct.SSHConfig()`. Keeping it at a single call site
is what prevents the two-insecure-call-sites problem from recurring. The `~/.ssh/config`
route discussed under "interaction with Fix 03" no longer applies: Fix 03 is superseded.

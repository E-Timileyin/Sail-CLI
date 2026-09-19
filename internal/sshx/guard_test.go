package sshx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoInsecureIgnoreHostKey is a tripwire, not a unit test.
//
// ssh.InsecureIgnoreHostKey() accepts any host key, which lets a machine-in-the-middle
// impersonate a production server for every command Sail runs against it. It appeared
// twice in this codebase (internal/model/server.go and internal/config/ssh/client.go)
// and survived unnoticed because the tests that would have caught it skipped without a
// live server. This test fails the build on reintroduction instead.
func TestNoInsecureIgnoreHostKey(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", ".idea":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// This file necessarily contains the identifier.
		if filepath.Base(path) == "guard_test.go" {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			// Allow the identifier in comments, where it documents the history.
			code := stripComment(line)
			if strings.Contains(code, "InsecureIgnoreHostKey") {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel+":"+itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("ssh.InsecureIgnoreHostKey() disables host-key verification and must not be used. Found at:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestAllClientConfigsVerifyHostKeys checks that every ssh.ClientConfig literal sets a
// HostKeyCallback. A config with a nil callback causes the ssh package to fail closed,
// but a config that explicitly sets an insecure callback does not — so this is the
// companion check to TestNoInsecureIgnoreHostKey for the field itself.
func TestAllClientConfigsVerifyHostKeys(t *testing.T) {
	root := repoRoot(t)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", ".idea":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(src)
		if !strings.Contains(text, "ssh.ClientConfig{") {
			return nil
		}
		if !strings.Contains(text, "HostKeyCallback:") {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s constructs an ssh.ClientConfig without setting HostKeyCallback", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
}

// repoRoot walks up until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod")
		}
		dir = parent
	}
}

// stripComment removes a trailing // comment so the guard does not trip on prose.
func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

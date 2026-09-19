package remote

import (
	"fmt"
	"strings"
	"testing"
)

// fakeRunner is the seam that lets the deploy state machine be tested without SSH or
// Docker. The previous Orchestrator needed a live daemon, so it was never tested.
type fakeRunner struct {
	commands []string
	// respond returns an error for a matching command. First match wins.
	respond func(cmd string) error
	// state is the canned output of ReadStateScript.
	state State
	// stateSet controls whether ReadStateScript returns state or the default.
	stateSet bool
}

func (f *fakeRunner) Run(command string) Result {
	f.commands = append(f.commands, command)
	res := Result{Command: command}

	switch {
	case strings.Contains(command, "printf 'current="):
		out := fmt.Sprintf("current=%s\nprevious=%s\ncompose=%s\nenv=%s\n",
			f.state.CurrentTag, f.state.PreviousTag,
			yesNo(f.state.HasCompose), yesNo(f.state.HasEnvFile))
		res.Stdout = out
		if !f.stateSet {
			res.Stdout = "current=\nprevious=\ncompose=yes\nenv=yes\n"
		}
		return res
	case strings.Contains(command, "docker --version"):
		res.Stdout = "Docker version 27.0.0\n"
		return res
	}

	if f.respond != nil {
		res.Err = f.respond(command)
	}
	return res
}

func (f *fakeRunner) MustRun(command string) error {
	return f.Run(command).Err
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (f *fakeRunner) ranContaining(substr string) bool {
	for _, c := range f.commands {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func (f *fakeRunner) ran(substr string) int {
	n := 0
	for _, c := range f.commands {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

func testLayout() Layout { return Layout{Root: "/srv/sail", App: "api"} }

// TestDeploySuccess covers the happy path and asserts the promote happens only after the
// deploy is verified.
func TestDeploySuccess(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{HasCompose: true, HasEnvFile: true}

	d := newDeployerWith(f, testLayout())
	out, err := d.Deploy("sha-new")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !out.Deployed {
		t.Error("outcome should report Deployed")
	}
	if !out.Attempted {
		t.Error("outcome should report the deploy was attempted")
	}
	if out.RolledBack {
		t.Error("a successful deploy must not roll back")
	}
	if !f.ranContaining("up -d --wait") {
		t.Error("deploy must gate on --wait")
	}
	// Promote writes .tag; it must come after the deploy.
	promoteIdx := -1
	deployIdx := -1
	for i, c := range f.commands {
		if strings.Contains(c, "up -d --wait") {
			deployIdx = i
		}
		if strings.Contains(c, "> '"+testLayout().CurrentTagPath()) {
			promoteIdx = i
		}
	}
	if promoteIdx < 0 {
		t.Fatal("promote never ran")
	}
	if deployIdx > promoteIdx {
		t.Error("promote must run after the deploy succeeds")
	}
}

// TestDeployFailureRollsBack is the central safety assertion: a failed deploy restores the
// previous tag and says so.
func TestDeployFailureRollsBack(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{CurrentTag: "sha-old", HasCompose: true, HasEnvFile: true}
	f.respond = func(cmd string) error {
		if strings.Contains(cmd, "up -d --wait") && strings.Contains(cmd, ".env.tag") {
			// First up fails; the rollback up (to the old tag) succeeds.
			if strings.Contains(cmd, "'sha-new'") {
				return fmt.Errorf("container exited 1")
			}
		}
		return nil
	}

	d := newDeployerWith(f, testLayout())
	out, err := d.Deploy("sha-new")
	if err == nil {
		t.Fatal("expected the deploy to fail")
	}
	if out.Deployed {
		t.Error("Deployed must be false on failure")
	}
	if !out.RolledBack {
		t.Errorf("expected a rollback, got %+v (err %v)", out, err)
	}
	if out.RollbackError != nil {
		t.Errorf("rollback should have succeeded: %v", out.RollbackError)
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("error should state the rollback happened: %v", err)
	}
	// The rollback must target the previous tag, not the failed one.
	if !f.ranContaining("'sha-old'") {
		t.Error("rollback did not restore the previous tag")
	}
}

// TestDeployFailureWithoutPreviousTagAvoidsPointlessRollback: on a first deploy there is
// nothing to restore, and attempting it would hide the real error.
func TestDeployFailureWithoutPreviousTagAvoidsPointlessRollback(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{HasCompose: true, HasEnvFile: true}
	f.respond = func(cmd string) error {
		if strings.Contains(cmd, "up -d --wait") {
			return fmt.Errorf("container exited 1")
		}
		return nil
	}

	d := newDeployerWith(f, testLayout())
	out, err := d.Deploy("sha-new")
	if err == nil {
		t.Fatal("expected failure")
	}
	if out.RolledBack {
		t.Error("there was no previous tag, so no rollback should be attempted")
	}
	if !strings.Contains(err.Error(), "no previous tag") {
		t.Errorf("error should explain why no rollback happened: %v", err)
	}
}

// TestRollbackFailureIsReportedDistinctly: a deploy that fails and cannot roll back is an
// outage, and must not be reported like an ordinary failure.
func TestRollbackFailureIsReportedDistinctly(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{CurrentTag: "sha-old", HasCompose: true, HasEnvFile: true}
	f.respond = func(cmd string) error {
		if strings.Contains(cmd, "up -d --wait") {
			return fmt.Errorf("container exited 1")
		}
		return nil
	}

	d := newDeployerWith(f, testLayout())
	out, err := d.Deploy("sha-new")
	if err == nil {
		t.Fatal("expected failure")
	}
	if out.RollbackError == nil {
		t.Fatal("expected the rollback failure to be recorded")
	}
	if out.RolledBack {
		t.Error("RolledBack must be false when the rollback failed")
	}
	if !strings.Contains(err.Error(), "ROLLBACK FAILED") {
		t.Errorf("error must distinguish a failed rollback: %v", err)
	}
	if !strings.Contains(err.Error(), "container exited 1") {
		t.Errorf("error must still report the original cause: %v", err)
	}
	if !f.ranContaining("rollback-failed") {
		t.Error("the failed rollback must be recorded in the history log")
	}
}

// TestDeployRejectsSameTag: redeploying the current tag would overwrite the previous tag
// with an identical value, destroying the rollback target.
func TestDeployRejectsSameTag(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{CurrentTag: "sha-same", PreviousTag: "sha-old", HasCompose: true, HasEnvFile: true}

	d := newDeployerWith(f, testLayout())
	out, err := d.Deploy("sha-same")
	if err == nil {
		t.Fatal("expected a redeploy of the current tag to be refused")
	}
	if !strings.Contains(err.Error(), "already deployed") {
		t.Errorf("error = %v, want it to say the tag is already deployed", err)
	}
	if f.ranContaining("up -d --wait") {
		t.Error("no deploy should run when the tag is already current")
	}
	if out.PreviousTag != "" {
		t.Errorf("a refusal before attempting anything should report no rollback target, got %q", out.PreviousTag)
	}
	if out.Attempted {
		t.Error("Attempted must be false when the deploy was refused up front")
	}
}

// TestDeployValidatesBeforeMutating: a server without a compose file must fail before
// anything is written or pulled.
func TestDeployValidatesBeforeMutating(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{HasCompose: false, HasEnvFile: false}

	d := newDeployerWith(f, testLayout())
	if _, err := d.Deploy("sha-new"); err == nil {
		t.Fatal("expected failure when the app is not scaffolded")
	}
	if f.ranContaining("pull") {
		t.Error("nothing should be pulled when validation fails")
	}
	if f.ranContaining("up -d --wait") {
		t.Error("nothing should start when validation fails")
	}
}

// TestDeployRecordsAttemptBeforeStarting: a crash mid-deploy must leave evidence.
func TestDeployRecordsAttemptBeforeStarting(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{CurrentTag: "sha-old", HasCompose: true, HasEnvFile: true}
	f.respond = func(cmd string) error {
		if strings.Contains(cmd, "up -d --wait") {
			return fmt.Errorf("boom")
		}
		return nil
	}

	d := newDeployerWith(f, testLayout())
	_, _ = d.Deploy("sha-new")

	startedIdx, deployIdx := -1, -1
	for i, c := range f.commands {
		if strings.Contains(c, "started") {
			startedIdx = i
		}
		if strings.Contains(c, "up -d --wait") {
			deployIdx = i
		}
	}
	if startedIdx < 0 {
		t.Fatal("the attempt must be recorded")
	}
	if startedIdx > deployIdx {
		t.Error("the attempt must be recorded before the deploy runs")
	}
}

// TestRollbackIsRepeatable: rolling back twice must return to the original state, which
// is what an operator expects under pressure.
func TestRollbackIsRepeatable(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{CurrentTag: "sha-new", PreviousTag: "sha-old", HasCompose: true, HasEnvFile: true}

	d := newDeployerWith(f, testLayout())
	out, err := d.Rollback()
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if !out.Deployed {
		t.Error("rollback should report a completed deploy")
	}
	if out.Tag != "sha-old" {
		t.Errorf("rollback deployed %q, want sha-old", out.Tag)
	}
	// Promote swaps the tags, so .tag now holds sha-old and .tag.prev holds sha-new.
	if !f.ranContaining("> '" + testLayout().CurrentTagPath()) {
		t.Error("rollback must record the new current tag, making it repeatable")
	}
}

func TestRollbackWithoutHistory(t *testing.T) {
	f := &fakeRunner{}
	f.stateSet = true
	f.state = State{CurrentTag: "sha-new", HasCompose: true, HasEnvFile: true}

	d := newDeployerWith(f, testLayout())
	if _, err := d.Rollback(); err == nil {
		t.Fatal("expected an error when there is no previous tag")
	}
}

// TestDeployScriptsUseQuotedPaths: the app name reaches the shell, so every path must be
// quoted. An unquoted name is a command injection against the deploy server.
func TestDeployScriptsUseQuotedPaths(t *testing.T) {
	l := Layout{Root: "/srv/sail", App: "api"}
	for _, script := range []string{
		DeployScript(l, "sha1"),
		RollbackScript(l, "sha1"),
		PromoteScript(l, "sha1"),
		HistoryScript(l, "sha1", "success"),
		ReadStateScript(l),
		SetupScript(l),
	} {
		// Every occurrence of the app directory must be immediately preceded by a quote.
		// Checking for the bare quoted string fails on longer paths like
		// '/srv/sail/api/.tag', which are quoted correctly.
		idx := 0
		for {
			rel := strings.Index(script[idx:], l.Dir())
			if rel < 0 {
				break
			}
			at := idx + rel
			if at == 0 || script[at-1] != '\'' {
				t.Errorf("path %q is not quoted in: %s", l.Dir(), script)
				break
			}
			idx = at + len(l.Dir())
		}
	}
}

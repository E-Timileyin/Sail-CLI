package remote

import (
	"fmt"
	"strings"
)

// executor lets the deploy sequence be tested without SSH.
type executor interface {
	Run(command string) Result
	MustRun(command string) error
}

type Deployer struct {
	runner executor
	layout Layout
}

func NewDeployer(r *Runner, layout Layout) *Deployer {
	return &Deployer{runner: r, layout: layout}
}

func newDeployerWith(r executor, layout Layout) *Deployer {
	return &Deployer{runner: r, layout: layout}
}

// DeployOutcome records whether a rollback ran: a failed deploy that rolled back and one
// that could not are different results.
type DeployOutcome struct {
	Tag string
	// PreviousTag is the rollback target; meaningful only when Attempted is true.
	PreviousTag   string
	Attempted     bool
	Deployed      bool
	RolledBack    bool
	RollbackError error
	FailedStage   string
}

// Deploy runs preflight, reads state, deploys, then promotes or rolls back.
// Promote happens only after --wait passes, so a failed deploy leaves the previous tag
// recorded as current — which is what makes rollback meaningful.
func (d *Deployer) Deploy(targetTag string) (*DeployOutcome, error) {
	out := &DeployOutcome{Tag: targetTag}

	out.FailedStage = "preflight"
	if err := d.runner.MustRun(PreflightScript()); err != nil {
		return out, fmt.Errorf("server is not ready (need docker and the compose v2 plugin): %w", err)
	}

	out.FailedStage = "read state"
	stateRes := d.runner.Run(ReadStateScript(d.layout))
	if stateRes.Err != nil {
		return out, fmt.Errorf("cannot read deployment state: %w", stateRes.Err)
	}
	state := ParseState(stateRes.Stdout)
	for _, line := range state.Unrecognised {
		// Surfaced rather than ignored: unexpected output usually means the remote shell
		// behaved differently than assumed, which invalidates the state read.
		return out, fmt.Errorf("unexpected output while reading deployment state: %q", line)
	}

	out.PreviousTag = state.CurrentTag

	if err := state.ValidateForDeploy(); err != nil {
		return out, err
	}

	// Refused so the previous tag is not overwritten with an identical value, which would
	// destroy the rollback target.
	if state.TargetIsCurrent(targetTag) {
		out.PreviousTag = ""
		return out, fmt.Errorf("tag %q is already deployed to app %q; nothing to do", targetTag, d.layout.App)
	}

	out.Attempted = true

	// Recorded before deploying: a crash mid-deploy must leave evidence.
	if err := d.appendHistory(targetTag, "started"); err != nil {
		return out, fmt.Errorf("cannot write to the deployment log on the server: %w", err)
	}

	out.FailedStage = "deploy"
	if err := d.runner.MustRun(DeployScript(d.layout, targetTag)); err != nil {
		return d.fail(out, fmt.Errorf("deploy of %q failed: %w", targetTag, err))
	}

	// --wait's exit status is the verification, so reaching here means healthy.
	out.FailedStage = "promote"
	if err := d.runner.MustRun(PromoteScript(d.layout, targetTag)); err != nil {
		return d.fail(out, fmt.Errorf("deploy succeeded but recording it failed: %w", err))
	}

	out.Deployed = true
	if err := d.appendHistory(targetTag, "success"); err != nil {
		// Only the record failed; rolling back a healthy deploy would be worse.
		return out, fmt.Errorf("deployed %q successfully but could not record it: %w", targetTag, err)
	}
	return out, nil
}

func (d *Deployer) fail(out *DeployOutcome, cause error) (*DeployOutcome, error) {
	if out.PreviousTag == "" {
		// Nothing to roll back to; attempting it would obscure the original error.
		_ = d.appendHistory(out.Tag, "failed-no-previous")
		return out, fmt.Errorf("%w (no previous tag to roll back to)", cause)
	}

	_ = d.appendHistory(out.Tag, "failed-rolling-back")

	if err := d.runner.MustRun(RollbackScript(d.layout, out.PreviousTag)); err != nil {
		out.RollbackError = err
		_ = d.appendHistory(out.Tag, "rollback-failed")
		// Both errors reported: the cause, and that the server is now in an unknown state.
		return out, fmt.Errorf(
			"%w\nROLLBACK FAILED: the server may be running no working version; expected %q. rollback error: %v",
			cause, out.PreviousTag, err)
	}

	out.RolledBack = true
	_ = d.appendHistory(out.Tag, "rolled-back-to-"+out.PreviousTag)
	return out, fmt.Errorf("%w (rolled back to %q)", cause, out.PreviousTag)
}

func (d *Deployer) Rollback() (*DeployOutcome, error) {
	out := &DeployOutcome{}

	stateRes := d.runner.Run(ReadStateScript(d.layout))
	if stateRes.Err != nil {
		return out, fmt.Errorf("cannot read deployment state: %w", stateRes.Err)
	}
	state := ParseState(stateRes.Stdout)

	if state.PreviousTag == "" {
		return out, fmt.Errorf("no previous tag recorded for app %q; nothing to roll back to", d.layout.App)
	}
	if state.TargetIsCurrent(state.PreviousTag) {
		return out, fmt.Errorf("previous tag %q is already current", state.PreviousTag)
	}

	out.Tag = state.PreviousTag
	out.PreviousTag = state.CurrentTag

	if err := d.runner.MustRun(RollbackScript(d.layout, state.PreviousTag)); err != nil {
		return out, fmt.Errorf("rollback to %q failed: %w", state.PreviousTag, err)
	}

	// Swap the tags so a second rollback returns to where you started.
	promote := PromoteScript(d.layout, state.PreviousTag)
	if err := d.runner.MustRun(promote); err != nil {
		return out, fmt.Errorf("rolled back to %q but could not record it: %w", state.PreviousTag, err)
	}

	out.Deployed = true
	if err := d.appendHistory(state.PreviousTag, "rollback"); err != nil {
		return out, fmt.Errorf("rolled back to %q but could not record it: %w", state.PreviousTag, err)
	}
	return out, nil
}

// appendHistory errors are returned, not swallowed: the log is how an operator
// reconstructs what happened.
func (d *Deployer) appendHistory(tag, outcome string) error {
	return d.runner.MustRun(HistoryScript(d.layout, tag, outcome))
}

func Trim(s string) string {
	s = strings.TrimSpace(s)
	const max = 2000
	if len(s) > max {
		return s[:max] + "... (truncated)"
	}
	return s
}

package remote

import (
	"fmt"
	"strconv"
	"strings"
)

// Layout is where an app lives on the server: apps/<name>/ under a root.
type Layout struct {
	Root string
	App  string
}

func (l Layout) Dir() string { return l.Root + "/" + l.App }

func (l Layout) File(name string) string { return l.Dir() + "/" + name }

func (l Layout) CurrentTagPath() string  { return l.File(".tag") }
func (l Layout) PreviousTagPath() string { return l.File(".tag.prev") }

// EnvPath holds secrets, chmod 600. Referenced, never written.
func (l Layout) EnvPath() string { return l.File(".env") }

func (l Layout) ComposePath() string { return l.File("compose.yaml") }
func (l Layout) LogPath() string     { return l.File("deploy.log") }

// TagEnvPath is written per deploy and passed to compose as a second --env-file.
func (l Layout) TagEnvPath() string { return l.File(".env.tag") }

// ImageRefPath avoids needing --image on every deploy.
func (l Layout) ImageRefPath() string { return l.File(".image") }

// compose builds a compose invocation. Both env files are required: without .env.tag,
// APP_TAG is unset and compose deploys whatever the compose file pins, reporting success.
func (l Layout) compose(sub string) string {
	return fmt.Sprintf("cd %s && docker compose -f %s --env-file %s --env-file %s %s",
		Quote(l.Dir()), Quote(l.ComposePath()), Quote(l.EnvPath()), Quote(l.TagEnvPath()), sub)
}

func NewLayout(root, app string) (Layout, error) {
	if err := ValidateAppName(app); err != nil {
		return Layout{}, err
	}
	if root == "" {
		return Layout{}, fmt.Errorf("remote root is required")
	}
	if !strings.HasPrefix(root, "/") && !strings.HasPrefix(root, "~") {
		return Layout{}, fmt.Errorf("remote root %q must be an absolute path or start with ~", root)
	}
	return Layout{Root: strings.TrimRight(root, "/"), App: app}, nil
}

// ValidateAppName rejects names that are not a single safe path segment.
// Rejected, not merely quoted: the name reaches a remote shell.
func ValidateAppName(app string) error {
	if app == "" {
		return fmt.Errorf("app name is required")
	}
	if len(app) > 63 {
		return fmt.Errorf("app name %q is too long (max 63 characters)", app)
	}
	for i, r := range app {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(app)-1 {
				return fmt.Errorf("app name %q must not start or end with a dash", app)
			}
		default:
			return fmt.Errorf(
				"invalid app name %q: use lowercase letters, digits and internal dashes (e.g. my-api)", app)
		}
	}
	return nil
}

func AppExistsScript(l Layout) string {
	return fmt.Sprintf("test -d %s && echo exists || echo absent", Quote(l.Dir()))
}

func ReadStateScript(l Layout) string {
	return fmt.Sprintf(
		"printf 'current=%%s\\n' \"$(cat %s 2>/dev/null || true)\"; "+
			"printf 'previous=%%s\\n' \"$(cat %s 2>/dev/null || true)\"; "+
			"printf 'compose=%%s\\n' \"$(test -f %s && echo yes || echo no)\"; "+
			"printf 'env=%%s\\n' \"$(test -f %s && echo yes || echo no)\"",
		Quote(l.CurrentTagPath()), Quote(l.PreviousTagPath()),
		Quote(l.ComposePath()), Quote(l.EnvPath()))
}

type State struct {
	CurrentTag   string
	PreviousTag  string
	HasCompose   bool
	HasEnvFile   bool
	RawOutput    string
	Unrecognised []string
}

func ParseState(out string) State {
	st := State{RawOutput: out}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found {
			st.Unrecognised = append(st.Unrecognised, line)
			continue
		}
		switch key {
		case "current":
			st.CurrentTag = val
		case "previous":
			st.PreviousTag = val
		case "compose":
			st.HasCompose = val == "yes"
		case "env":
			st.HasEnvFile = val == "yes"
		default:
			st.Unrecognised = append(st.Unrecognised, line)
		}
	}
	return st
}

func (st State) ValidateForDeploy() error {
	if !st.HasCompose {
		return fmt.Errorf("no compose.yaml on the server; run `sail app new` to scaffold the app first")
	}
	if !st.HasEnvFile {
		return fmt.Errorf("no .env on the server; run `sail app new` so secrets are in place (chmod 600)")
	}
	return nil
}

func (st State) TargetIsCurrent(tag string) bool {
	return st.CurrentTag != "" && st.CurrentTag == tag
}

// PreflightScript checks the docker CLI and the compose v2 plugin.
// v1 (docker-compose) is not accepted: it lacks `up -d --wait`, so falling back to it
// would silently remove the health gate.
func PreflightScript() string {
	return "docker --version && docker compose version"
}

// DeployScript writes the target tag, pulls, then starts with --wait.
// The tag is written before pull because pull resolves APP_TAG from the override file.
func DeployScript(l Layout, targetTag string) string {
	return strings.Join([]string{
		WriteEnvTag(l, targetTag),
		l.compose("pull"),
		l.compose("up -d --wait"),
	}, " && ")
}

func RollbackScript(l Layout, tag string) string {
	return strings.Join([]string{
		WriteEnvTag(l, tag),
		l.compose("up -d --wait"),
	}, " && ")
}

func WriteEnvTag(l Layout, tag string) string {
	return fmt.Sprintf("printf 'APP_TAG=%%s\\n' %s > %s && chmod 600 %s",
		Quote(tag), Quote(l.TagEnvPath()), Quote(l.TagEnvPath()))
}

// PromoteScript records a verified deploy.
//
// Previous is written first so an interruption leaves a usable rollback target.
func PromoteScript(l Layout, targetTag string) string {
	return strings.Join([]string{
		fmt.Sprintf("if [ -f %s ]; then cp %s %s.tmp && mv %s.tmp %s; fi",
			Quote(l.CurrentTagPath()), Quote(l.CurrentTagPath()),
			Quote(l.PreviousTagPath()), Quote(l.PreviousTagPath()), Quote(l.PreviousTagPath())),
		fmt.Sprintf("printf '%%s\\n' %s > %s.tmp && mv %s.tmp %s",
			Quote(targetTag), Quote(l.CurrentTagPath()),
			Quote(l.CurrentTagPath()), Quote(l.CurrentTagPath())),
	}, " && ")
}

func HistoryScript(l Layout, targetTag, outcome string) string {
	return fmt.Sprintf("printf '%%s\\t%%s\\t%%s\\n' \"$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)\" %s %s >> %s",
		Quote(targetTag), Quote(outcome), Quote(l.LogPath()))
}

func SetupScript(l Layout) string {
	return fmt.Sprintf("mkdir -p %s && chmod 700 %s", Quote(l.Dir()), Quote(l.Dir()))
}

func ReadLogScript(l Layout, n int) string {
	if n <= 0 {
		n = 20
	}
	return fmt.Sprintf("test -f %s && tail -n %s %s || echo 'no deployment history'",
		Quote(l.LogPath()), Quote(strconv.Itoa(n)), Quote(l.LogPath()))
}

func LogsScript(l Layout, n int, follow bool) string {
	if n <= 0 {
		n = 100
	}
	flag := ""
	if follow {
		flag = " -f"
	}
	return l.compose("logs --tail " + strconv.Itoa(n) + flag)
}

func StatusScript(l Layout) string {
	return l.compose("ps")
}

func SetImageRefScript(l Layout, image string) string {
	return fmt.Sprintf("printf '%%s\\n' %s > %s.tmp && mv %s.tmp %s && chmod 600 %s",
		Quote(image), Quote(l.ImageRefPath()), Quote(l.ImageRefPath()),
		Quote(l.ImageRefPath()), Quote(l.ImageRefPath()))
}

func ReadImageRefScript(l Layout) string {
	return fmt.Sprintf("cat %s 2>/dev/null || true", Quote(l.ImageRefPath()))
}

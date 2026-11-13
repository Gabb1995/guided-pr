package forge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Tool describes a forge CLI that guided-pr shells out to.
type Tool struct {
	Name    string // "glab" or "gh"
	Forge   string // "GitLab" or "GitHub"
	Install string
	Login   string
}

var (
	Glab = Tool{Name: "glab", Forge: "GitLab", Install: "https://gitlab.com/gitlab-org/cli#installation", Login: "glab auth login"}
	GH   = Tool{Name: "gh", Forge: "GitHub", Install: "https://cli.github.com", Login: "gh auth login"}
)

// ToolFor returns the CLI used for a provider.
func ToolFor(provider string) Tool {
	if provider == ProviderGitLab {
		return Glab
	}
	return GH
}

// Check reports whether the CLI is installed and logged in, with a fix if not.
func (t Tool) Check(ctx context.Context) error {
	if _, err := exec.LookPath(t.Name); err != nil {
		return fmt.Errorf("%s needs the %s CLI, which is not installed. Install it from %s, then run `%s`", t.Forge, t.Name, t.Install, t.Login)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, t.Name, "auth", "status").CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 300 {
			detail = detail[:300] + "…"
		}
		return fmt.Errorf("the %s CLI is installed but not logged in to %s. Run `%s` (it said: %s)", t.Name, t.Forge, t.Login, detail)
	}
	return nil
}

// explain turns a failed CLI call into an actionable error when the CLI is missing or logged out.
func explain(t Tool, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s needs the %s CLI, which is not installed. Install it from %s, then run `%s`", t.Forge, t.Name, t.Install, t.Login)
	}
	msg := strings.ToLower(err.Error())
	for _, hint := range []string{"401", "unauthorized", "not logged", "authentication", "auth login", "token"} {
		if strings.Contains(msg, hint) {
			return fmt.Errorf("%w\n\nThis looks like a login problem: run `%s` and try again", err, t.Login)
		}
	}
	return err
}

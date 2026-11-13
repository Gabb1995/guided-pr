package forge

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"testing"
)

func TestParsePullRequestURL(t *testing.T) {
	gh, err := ParsePullRequestURL("https://github.com/acme/payments/pull/42/files")
	if err != nil || gh.Owner != "acme" || gh.Repo != "payments" || gh.Number != 42 {
		t.Fatalf("github: %#v %v", gh, err)
	}
	gl, err := ParsePullRequestURL("https://gitlab.com/acme/platform/payments/-/merge_requests/123/diffs")
	if err != nil || gl.Provider != ProviderGitLab || gl.Owner != "acme/platform" || gl.Repo != "payments" || gl.Number != 123 {
		t.Fatalf("gitlab: %#v %v", gl, err)
	}
	for _, bad := range []string{"https://gitlab.com/portal/-/merge_requests/1", "https://example.com/a/b/pull/1", "http://github.com/a/b/pull/1"} {
		if _, err := ParsePullRequestURL(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestHunkLinesTrackBothSides(t *testing.T) {
	h := SplitHunks("a.go", "@@ -5,2 +5,2 @@\n ctx\n-old\n+new")[0]
	lines := HunkLines(h)
	if len(lines) != 3 || lines[1].Side() != "old" || lines[1].Number() != 6 || lines[2].Side() != "new" || lines[2].Number() != 6 || lines[2].OldPos != 7 {
		t.Fatalf("unexpected lines: %+v", lines)
	}
}

func TestGitLabRangePointUsesLineCode(t *testing.T) {
	sum := sha1.Sum([]byte("a.go"))
	p := gitlabRangePoint("a.go", DiffLine{Kind: "add", OldPos: 7, NewPos: 6})
	if p["line_code"] != hex.EncodeToString(sum[:])+"_7_6" || p["type"] != "new" || p["new_line"] != 6 || p["old_line"] != nil {
		t.Fatalf("unexpected range point: %v", p)
	}
	ctx := gitlabRangePoint("a.go", DiffLine{Kind: "ctx", OldPos: 5, NewPos: 5})
	if ctx["type"] != nil || ctx["old_line"] != 5 || ctx["new_line"] != 5 {
		t.Fatalf("unexpected context point: %v", ctx)
	}
}

func TestMissingCLIErrorsSayHowToFixIt(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := GlabCLI{}.LoadPullRequest(context.Background(), PullRequestURL{Provider: ProviderGitLab, Owner: "a", Repo: "b", Number: 1})
	if err == nil || !strings.Contains(err.Error(), "glab auth login") || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("unhelpful error: %v", err)
	}
	if err := GH.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "https://cli.github.com") {
		t.Fatalf("unhelpful check: %v", err)
	}
}

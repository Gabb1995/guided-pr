package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// GHCLI talks to github.com through the authenticated `gh` CLI.
type GHCLI struct{}

func (GHCLI) LoadPullRequest(ctx context.Context, reference PullRequestURL) (PullRequest, []ChangedFile, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d", reference.Owner, reference.Repo, reference.Number)
	var raw struct {
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
		Base    struct {
			SHA string `json:"sha"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := ghJSON(ctx, endpoint, &raw); err != nil {
		return PullRequest{}, nil, err
	}
	var files []struct {
		Filename         string `json:"filename"`
		Status           string `json:"status"`
		PreviousFilename string `json:"previous_filename"`
		Additions        int    `json:"additions"`
		Deletions        int    `json:"deletions"`
		Patch            string `json:"patch"`
	}
	if err := ghJSON(ctx, endpoint+"/files?per_page=100", &files, "--paginate"); err != nil {
		return PullRequest{}, nil, err
	}
	changed := make([]ChangedFile, 0, len(files))
	for _, file := range files {
		changed = append(changed, ChangedFile{Path: file.Filename, Status: file.Status, Previous: file.PreviousFilename, Additions: file.Additions, Deletions: file.Deletions, Hunks: SplitHunks(file.Filename, file.Patch)})
	}
	return PullRequest{Provider: ProviderGitHub, Owner: reference.Owner, Repo: reference.Repo, Number: reference.Number, Title: raw.Title, HTMLURL: raw.HTMLURL, Description: raw.Body, SourceBranch: raw.Head.Ref, BaseSHA: raw.Base.SHA, HeadSHA: raw.Head.SHA}, changed, nil
}

func (GHCLI) ReadFile(ctx context.Context, pr PullRequest, sha, path string) ([]byte, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", pr.Owner, pr.Repo, escapeSegments(path), url.QueryEscape(sha))
	return ghRaw(ctx, "api", endpoint, "-H", "Accept: application/vnd.github.raw")
}

// SubmitReview creates one review holding every comment and the verdict, so it lands all at once or not at all.
func (GHCLI) SubmitReview(ctx context.Context, pr PullRequest, review Review) error {
	event := map[string]string{VerdictApprove: "APPROVE", VerdictComment: "COMMENT", VerdictRequestChanges: "REQUEST_CHANGES"}[review.Verdict]
	if event == "" {
		return fmt.Errorf("unknown verdict %q", review.Verdict)
	}
	comments := make([]map[string]any, 0, len(review.Comments))
	for _, c := range review.Comments {
		item := map[string]any{"path": c.Path, "body": c.Body, "line": c.End.Number(), "side": githubSide(c.End)}
		if c.Start != c.End {
			item["start_line"], item["start_side"] = c.Start.Number(), githubSide(c.Start)
		}
		comments = append(comments, item)
	}
	body := map[string]any{"commit_id": pr.HeadSHA, "event": event, "comments": comments}
	if review.Body != "" {
		body["body"] = review.Body
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode GitHub request: %w", err)
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", pr.Owner, pr.Repo, pr.Number)
	command := exec.CommandContext(ctx, "gh", "api", endpoint, "--method", "POST", "--input", "-")
	command.Stdin = bytes.NewReader(payload)
	if output, err := command.CombinedOutput(); err != nil {
		return explain(GH, fmt.Errorf("GitHub CLI request failed: %w: %s", err, string(output)))
	}
	return nil
}

func githubSide(l DiffLine) string {
	if l.Side() == "old" {
		return "LEFT"
	}
	return "RIGHT"
}

func escapeSegments(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func ghJSON(ctx context.Context, endpoint string, target any, arguments ...string) error {
	output, err := ghRaw(ctx, append([]string{"api", endpoint}, arguments...)...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(output, target); err != nil {
		return fmt.Errorf("decode GitHub CLI response: %w", err)
	}
	return nil
}

func ghRaw(ctx context.Context, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "gh", arguments...)
	output, err := command.Output()
	if err != nil {
		detail := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			detail = string(exitErr.Stderr)
		}
		return nil, explain(GH, fmt.Errorf("GitHub CLI request failed: %w: %s %s", err, detail, string(output)))
	}
	return output, nil
}

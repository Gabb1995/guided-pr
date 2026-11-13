package forge

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// GlabCLI talks to gitlab.com through the authenticated `glab` CLI.
type GlabCLI struct{}

const gitlabPageSize = 100

func gitlabProject(owner, repo string) string {
	return "projects/" + url.PathEscape(owner+"/"+repo)
}

func (GlabCLI) LoadPullRequest(ctx context.Context, reference PullRequestURL) (PullRequest, []ChangedFile, error) {
	endpoint := fmt.Sprintf("%s/merge_requests/%d", gitlabProject(reference.Owner, reference.Repo), reference.Number)
	var raw struct {
		Title        string `json:"title"`
		WebURL       string `json:"web_url"`
		Description  string `json:"description"`
		SourceBranch string `json:"source_branch"`
		SHA          string `json:"sha"`
		DiffRefs     struct {
			BaseSHA  string `json:"base_sha"`
			HeadSHA  string `json:"head_sha"`
			StartSHA string `json:"start_sha"`
		} `json:"diff_refs"`
	}
	if err := glabJSON(ctx, endpoint, &raw); err != nil {
		return PullRequest{}, nil, err
	}
	headSHA := raw.DiffRefs.HeadSHA
	if headSHA == "" {
		headSHA = raw.SHA
	}
	type diff struct {
		OldPath     string `json:"old_path"`
		NewPath     string `json:"new_path"`
		NewFile     bool   `json:"new_file"`
		RenamedFile bool   `json:"renamed_file"`
		DeletedFile bool   `json:"deleted_file"`
		Diff        string `json:"diff"`
	}
	var diffs []diff
	for page := 1; ; page++ {
		var batch []diff
		if err := glabJSON(ctx, fmt.Sprintf("%s/diffs?per_page=%d&page=%d", endpoint, gitlabPageSize, page), &batch); err != nil {
			return PullRequest{}, nil, err
		}
		diffs = append(diffs, batch...)
		if len(batch) < gitlabPageSize {
			break
		}
	}
	changed := make([]ChangedFile, 0, len(diffs))
	for _, file := range diffs {
		status, previous := "modified", ""
		switch {
		case file.NewFile:
			status = "added"
		case file.DeletedFile:
			status = "removed"
		case file.RenamedFile:
			status, previous = "renamed", file.OldPath
		}
		patch := strings.TrimSuffix(file.Diff, "\n")
		additions, deletions := countChanges(patch)
		changed = append(changed, ChangedFile{Path: file.NewPath, Status: status, Previous: previous, Additions: additions, Deletions: deletions, Hunks: SplitHunks(file.NewPath, patch)})
	}
	return PullRequest{
		Provider: ProviderGitLab, Owner: reference.Owner, Repo: reference.Repo, Number: reference.Number,
		Title: raw.Title, HTMLURL: raw.WebURL, Description: raw.Description, SourceBranch: raw.SourceBranch,
		BaseSHA: raw.DiffRefs.BaseSHA, StartSHA: raw.DiffRefs.StartSHA, HeadSHA: headSHA,
	}, changed, nil
}

func (GlabCLI) ReadFile(ctx context.Context, pr PullRequest, sha, path string) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/repository/files/%s/raw?ref=%s", gitlabProject(pr.Owner, pr.Repo), url.PathEscape(path), url.QueryEscape(sha))
	output, err := exec.CommandContext(ctx, "glab", "api", endpoint).Output()
	if err != nil {
		detail := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			detail = string(exitErr.Stderr)
		}
		return nil, explain(Glab, fmt.Errorf("GitLab CLI request failed: %w: %s", err, detail))
	}
	return output, nil
}

// CreateComment starts a diff discussion; a range uses GitLab's line_range with line codes.
func (GlabCLI) CreateComment(ctx context.Context, pr PullRequest, comment Comment) error {
	endpoint := fmt.Sprintf("%s/merge_requests/%d/discussions", gitlabProject(pr.Owner, pr.Repo), pr.Number)
	startSHA := pr.StartSHA
	if startSHA == "" {
		startSHA = pr.BaseSHA
	}
	oldPath := comment.OldPath
	if oldPath == "" {
		oldPath = comment.Path
	}
	position := map[string]any{
		"position_type": "text", "base_sha": pr.BaseSHA, "start_sha": startSHA, "head_sha": comment.CommitSHA,
		"new_path": comment.Path, "old_path": oldPath,
	}
	// The anchor is the last line. Added line: new_line only. Removed line: old_line only. Context: both.
	for key, value := range gitlabLineNumbers(comment.End) {
		position[key] = value
	}
	if comment.Start != comment.End {
		position["line_range"] = map[string]any{"start": gitlabRangePoint(comment.Path, comment.Start), "end": gitlabRangePoint(comment.Path, comment.End)}
	}
	return glabWriteJSON(ctx, "POST", endpoint, map[string]any{"body": comment.Body, "position": position})
}

func gitlabLineNumbers(l DiffLine) map[string]any {
	switch l.Kind {
	case "add":
		return map[string]any{"new_line": l.NewPos}
	case "del":
		return map[string]any{"old_line": l.OldPos}
	default:
		return map[string]any{"new_line": l.NewPos, "old_line": l.OldPos}
	}
}

func gitlabRangePoint(path string, l DiffLine) map[string]any {
	digest := sha1.Sum([]byte(path))
	point := gitlabLineNumbers(l)
	point["line_code"] = fmt.Sprintf("%s_%d_%d", hex.EncodeToString(digest[:]), l.OldPos, l.NewPos)
	point["type"] = map[string]any{"add": "new", "del": "old"}[l.Kind]
	if l.Kind == "ctx" {
		point["type"] = nil
	}
	return point
}

func glabJSON(ctx context.Context, endpoint string, target any) error {
	output, err := exec.CommandContext(ctx, "glab", "api", endpoint).Output()
	if err != nil {
		detail := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			detail = string(exitErr.Stderr)
		}
		return explain(Glab, fmt.Errorf("GitLab CLI request failed: %w: %s", err, detail))
	}
	if err := json.Unmarshal(output, target); err != nil {
		return fmt.Errorf("decode GitLab CLI response: %w", err)
	}
	return nil
}

// glabWriteJSON sends body as a JSON request body on stdin, which supports nested objects.
func glabWriteJSON(ctx context.Context, method, endpoint string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode GitLab request: %w", err)
	}
	command := exec.CommandContext(ctx, "glab", "api", endpoint, "-X", method, "-H", "Content-Type: application/json", "--input", "-")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		return explain(Glab, fmt.Errorf("GitLab CLI request failed: %w: %s", err, string(output)))
	}
	return nil
}

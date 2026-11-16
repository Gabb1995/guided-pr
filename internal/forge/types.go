// Package forge talks to GitLab (glab) and GitHub (gh) using the CLIs' existing authentication.
package forge

import "context"

type PullRequestURL struct {
	Provider string
	Owner    string
	Repo     string
	Number   int
}

type PullRequest struct {
	Provider     string `json:"provider"`
	Owner        string `json:"owner"`
	Repo         string `json:"repo"`
	Number       int    `json:"number"`
	Title        string `json:"title"`
	HTMLURL      string `json:"htmlUrl"`
	Description  string `json:"description"`
	SourceBranch string `json:"sourceBranch,omitempty"`
	BaseSHA      string `json:"baseSha"`
	StartSHA     string `json:"startSha,omitempty"`
	HeadSHA      string `json:"headSha"`
}

type Hunk struct {
	ID       string `json:"id"`
	OldStart int    `json:"oldStart"`
	NewStart int    `json:"newStart"`
	Patch    string `json:"patch"`
}

type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Previous  string `json:"previousPath,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Hunks     []Hunk `json:"hunks"`
}

// Comment is a review comment on one line or a range of lines of one hunk.
type Comment struct {
	Path      string
	OldPath   string
	Body      string
	CommitSHA string
	Start     DiffLine
	End       DiffLine
}

type Forge interface {
	LoadPullRequest(context.Context, PullRequestURL) (PullRequest, []ChangedFile, error)
	ReadFile(ctx context.Context, pr PullRequest, sha, path string) ([]byte, error)
	// SubmitReview sends the review in one go: every comment, the summary and the verdict.
	SubmitReview(context.Context, PullRequest, Review) error
}

// Verdicts a review can be submitted with. RequestChanges is GitHub only.
const (
	VerdictApprove        = "approve"
	VerdictComment        = "comment"
	VerdictRequestChanges = "request_changes"
)

// Review is a batch of comments plus an optional summary, submitted with one verdict on the reviewed commit.
type Review struct {
	Verdict  string
	Body     string
	Comments []Comment
}

// HostName is the human-readable forge name.
func (p PullRequest) HostName() string {
	if p.Provider == ProviderGitLab {
		return "GitLab"
	}
	return "GitHub"
}

func (p PullRequest) Reference() PullRequestURL {
	return PullRequestURL{Provider: p.Provider, Owner: p.Owner, Repo: p.Repo, Number: p.Number}
}

// Forges dispatches to the GitHub or GitLab client by provider.
type Forges struct {
	GitHub Forge
	GitLab Forge
}

func (f Forges) pick(provider string) (Forge, error) {
	if provider == ProviderGitLab && f.GitLab != nil {
		return f.GitLab, nil
	}
	if provider == ProviderGitHub && f.GitHub != nil {
		return f.GitHub, nil
	}
	return nil, errUnsupported(provider)
}

func (f Forges) LoadPullRequest(ctx context.Context, r PullRequestURL) (PullRequest, []ChangedFile, error) {
	c, err := f.pick(r.Provider)
	if err != nil {
		return PullRequest{}, nil, err
	}
	return c.LoadPullRequest(ctx, r)
}

func (f Forges) ReadFile(ctx context.Context, pr PullRequest, sha, path string) ([]byte, error) {
	c, err := f.pick(pr.Provider)
	if err != nil {
		return nil, err
	}
	return c.ReadFile(ctx, pr, sha, path)
}

func (f Forges) SubmitReview(ctx context.Context, pr PullRequest, review Review) error {
	c, err := f.pick(pr.Provider)
	if err != nil {
		return err
	}
	return c.SubmitReview(ctx, pr, review)
}

type errUnsupported string

func (e errUnsupported) Error() string { return "no forge configured for provider " + string(e) }

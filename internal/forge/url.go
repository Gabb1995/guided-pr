package forge

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
)

// ParsePullRequestURL accepts GitHub pull requests
// (https://github.com/OWNER/REPO/pull/N) and GitLab merge requests
// (https://gitlab.com/GROUP[/SUBGROUP...]/PROJECT/-/merge_requests/N).
func ParsePullRequestURL(raw string) (PullRequestURL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return PullRequestURL{}, fmt.Errorf("parse pull request URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return PullRequestURL{}, fmt.Errorf("pull request URL must use https")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	switch strings.ToLower(parsed.Hostname()) {
	case "github.com":
		if len(parts) < 4 || parts[2] != "pull" || parts[0] == "" || parts[1] == "" {
			return PullRequestURL{}, fmt.Errorf("pull request URL must look like https://github.com/OWNER/REPO/pull/NUMBER")
		}
		number, err := positiveNumber(parts[3])
		if err != nil {
			return PullRequestURL{}, err
		}
		return PullRequestURL{Provider: ProviderGitHub, Owner: parts[0], Repo: parts[1], Number: number}, nil
	case "gitlab.com":
		marker := -1
		for index := 0; index+2 < len(parts); index++ {
			if parts[index] == "-" && parts[index+1] == "merge_requests" {
				marker = index
				break
			}
		}
		if marker < 2 {
			return PullRequestURL{}, fmt.Errorf("merge request URL must look like https://gitlab.com/GROUP/PROJECT/-/merge_requests/NUMBER")
		}
		for _, segment := range parts[:marker] {
			if segment == "" {
				return PullRequestURL{}, fmt.Errorf("merge request URL has an empty path segment")
			}
		}
		number, err := positiveNumber(parts[marker+2])
		if err != nil {
			return PullRequestURL{}, err
		}
		return PullRequestURL{Provider: ProviderGitLab, Owner: strings.Join(parts[:marker-1], "/"), Repo: parts[marker-1], Number: number}, nil
	}
	return PullRequestURL{}, fmt.Errorf("pull request URL must use github.com or gitlab.com")
}

func positiveNumber(raw string) (int, error) {
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("pull request number must be a positive integer")
	}
	return number, nil
}

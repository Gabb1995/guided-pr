package deck

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gabb1995/guided-pr/internal/forge"
)

func TestTicketKeysComeFromTitleAndBranch(t *testing.T) {
	pr := forge.PullRequest{Title: "PAY-1070 Audit log", SourceBranch: "feature/PAY/PAY-1070-audit-log", Description: "see OPS-1"}
	got := TicketKeys(pr)
	if len(got) != 1 || got[0] != "PAY-1070" {
		t.Fatalf("TicketKeys = %v, want [PAY-1070]", got)
	}
}

func TestTicketIsRequiredWhenTheMRNamesOne(t *testing.T) {
	keys := []string{"PAY-1070"}
	loaded := &Ticket{Key: "PAY-1070", URL: "https://x.atlassian.net/browse/PAY-1070", Title: "Audit log"}
	cases := map[string]struct {
		intro   Intro
		keys    []string
		wantErr string
	}{
		"loaded ticket":          {Intro{Ticket: loaded}, keys, ""},
		"no key in the MR":       {Intro{}, nil, ""},
		"could not be loaded":    {Intro{TicketUnavailable: "no Jira access"}, keys, ""},
		"skipped":                {Intro{}, keys, "the MR names ticket PAY-1070"},
		"guessed, no title":      {Intro{Ticket: &Ticket{Key: "PAY-1070", URL: "u"}}, keys, "needs key, url and title"},
		"both ticket and reason": {Intro{Ticket: loaded, TicketUnavailable: "x"}, keys, "not both"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var errs []string
			validateTicket(c.intro, c.keys, func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) })
			got := strings.Join(errs, "; ")
			if c.wantErr == "" && got != "" || c.wantErr != "" && !strings.Contains(got, c.wantErr) {
				t.Fatalf("errors = %q, want %q", got, c.wantErr)
			}
		})
	}
}

# Guided PR

A local app that presents a GitLab merge request or GitHub pull request like the author walking you
through it: a short intro, then one focused slide at a time with the code on the left and the
explanation on the right. Select lines and ask about them; the AI agent that wrote the walkthrough
answers live, with new views of the code. It can also draft a review comment for you to post.

Guided PR never calls an AI provider. An agent session (Claude Code with the `guided-pr-review`
skill) uses its own auth to write the deck and to answer questions through the `guided-pr` CLI.

## Install

**Requirement:** guided-pr talks to GitLab and GitHub through their official CLIs and your existing
login, so it never stores a token. Install and log in to the one(s) you need:

| Reviewing on | Install | Log in |
|---|---|---|
| GitLab | [`glab`](https://gitlab.com/gitlab-org/cli#installation) | `glab auth login` |
| GitHub | [`gh`](https://cli.github.com) | `gh auth login` |

`guided-pr doctor` checks both.

**1. The app** (one binary, no dependencies):

```bash
curl -fsSL https://raw.githubusercontent.com/Gabb1995/guided-pr/main/install.sh | sh
# or: go install github.com/Gabb1995/guided-pr/cmd/guided-pr@latest
# or: download a binary for your platform from Releases (Windows: the .zip)
```

**2. The agent skill**, for whichever agent you use:

- **Claude Code**, as a plugin:
  ```
  /plugin marketplace add Gabb1995/guided-pr
  /plugin install guided-pr@guided-pr
  ```
- **Claude Code or OpenCode**, without the plugin: `guided-pr install-skill`
  (writes the skill to `~/.claude/skills` and/or `~/.config/opencode/skills`).

**3. Use it.** From the repository the PR changes, ask your agent:

> Give me a guided review of https://github.com/OWNER/REPO/pull/123

It writes the walkthrough, opens it at `http://localhost:8787/reviews/…`, and stays connected to
answer what you ask in the page. Click **Finish** when you're done.

### In the page

- `←` `→` to move between slides; `/` to ask about the current slide.
- Drag over lines (or click, then shift-click) to select them, then **Ask** or **Comment**.
- Answers open as detour slides; **Where you asked** keeps your code one click away. `Esc` goes back.
- Draft comments are editable and only posted to GitLab/GitHub when you click **Post**.

## How it works

```
agent: guided-pr new URL ──▶ snapshot pinned to the head SHA (hunks with stable IDs)
agent: guided-pr deck ID deck.json ──▶ validated; code blocks fetched from the forge at that SHA
you:   open /reviews/ID, read, select lines, ask ──▶ question stored and queued
agent: guided-pr next ID (blocks until you ask) ──▶ investigates ──▶ guided-pr answer ID QID answer.json
you:   the answer appears as a detour slide; a draft comment can be edited and posted
you:   Finish ──▶ guided-pr next exits 4 and the agent stops listening
```

### CLI

| Command | Purpose |
|---|---|
| `guided-pr start` / `stop` / `status` | Run the server in the background on `127.0.0.1:8787` |
| `guided-pr serve [--addr] [--data-dir]` | Run the server in the foreground |
| `guided-pr install-skill [--claude] [--opencode]` | Install the agent skill matching this binary |
| `guided-pr doctor` | Check that `glab` / `gh` are installed and logged in |
| `guided-pr version` | Print the version |
| `guided-pr new URL` | Create a session from an MR/PR URL |
| `guided-pr bundle SESSION` | Print the snapshot (files, hunks, SHAs) |
| `guided-pr deck SESSION FILE` | Import a `review-deck.v1` deck |
| `guided-pr next SESSION [--wait 25m]` | Wait for the next question; exit 0 question, 3 timeout, 4 finished |
| `guided-pr status SESSION QID TEXT` | Show a progress line under the question |
| `guided-pr answer SESSION QID FILE` | Answer with `{"say": "...", "blocks": [...]}` |

### Deck format

`review-deck.v1` (see `internal/deck/deck.go` and the skill for the full contract): an `intro`
(title, story, ticket) and `chapters` of `slides` (`title`, `say`, `blocks`), plus a `minor` list
of trivial hunks per chapter. Slides and answers share one block vocabulary: `hunk`, `code`
(path + ref + line range, filled in by the server, never pasted), `diagram` (Mermaid), `compare`,
and `comment` (answers only: a draft the reviewer posts). Import fails unless every hunk is shown.

### Packages

| Package | Job |
|---|---|
| `internal/forge` | `glab`/`gh`: load the MR/PR, read a file at a SHA, post single-line or range comments |
| `internal/deck` | Deck and answer types, validation, resolving code blocks |
| `internal/session` | Per-session folder: `snapshot.json`, `deck.json`, append-only `events.jsonl` |
| `internal/qa` | Question queue, claims, agent presence, live fan-out |
| `internal/web` | HTTP API, server-side highlighting, SSE, embedded UI (`assets/`) |

## Data

`~/.local/share/guided-pr/sessions/<id>/` holds `snapshot.json` (never changes), `deck.json`, and
`events.jsonl` (questions, progress, answers, posted comments, finish). Reloading the page replays
the log, so detours survive. Directories are `0700`, files `0600`. 

## Security

- Loopback only; requests must have a `localhost`/`127.0.0.1`/`::1` `Host` (blocks DNS rebinding).
- Every write request must send `X-Guided-PR: 1` and a JSON body, which browsers can't do
  cross-origin without a preflight this server never approves.
- Same-origin scripts only (Mermaid is vendored); `nosniff`, no referrer, no framing.
- The agent can only draft comments. Posting happens from the page, after the server checks the
  branch hasn't moved since the snapshot.
- No credentials stored: forge calls go through `glab`/`gh`.

## Development

```bash
make build | make test | make run (foreground) | make start / stop
```

The skills live in `plugin/skills/guided-pr-review/SKILL.md` (Claude Code; also the plugin) and
`opencode/guided-pr-review/SKILL.md`; both are embedded in the binary. Releases are built by
GoReleaser when a `v*` tag is pushed; bump `plugin/.claude-plugin/plugin.json` `version` too.

Currently gitlab.com and github.com URLs are supported (not self-hosted GitLab or GitHub Enterprise).

## License

MIT

// Package guidedpr embeds the agent skills so the binary can install the version that matches it.
package guidedpr

import _ "embed"

// ClaudeSkill is the Claude Code skill (also shipped as the Claude Code plugin in ./plugin).
//
//go:embed plugin/skills/guided-pr-review/SKILL.md
var ClaudeSkill []byte

// OpenCodeSkill is the OpenCode variant: it waits for questions in the foreground instead of in the background.
//
//go:embed opencode/guided-pr-review/SKILL.md
var OpenCodeSkill []byte

// OpenCodeCommand adds /guided-pr-review to OpenCode's command menu; it hands the URL to the skill.
//
//go:embed opencode/commands/guided-pr-review.md
var OpenCodeCommand []byte

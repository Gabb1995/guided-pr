package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	guidedpr "github.com/Gabb1995/guided-pr"
	"github.com/Gabb1995/guided-pr/internal/forge"
)

// version is set at release time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func healthy(addr string) (string, bool) {
	c := http.Client{Timeout: time.Second}
	res, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	var h struct{ Version string }
	_ = json.NewDecoder(res.Body).Decode(&h)
	return h.Version, res.StatusCode == 200
}

// start runs `guided-pr serve` in the background, logging to the data directory.
func start(addr, dataDir string) {
	if v, ok := healthy(addr); ok {
		if v != version && version != "dev" {
			fmt.Printf("guided-pr %s is already running at http://%s (this binary is %s; run `guided-pr stop` then `guided-pr start` to switch)\n", v, addr, version)
			return
		}
		fmt.Printf("guided-pr already running at http://%s\n", addr)
		return
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		die(1, err.Error())
	}
	logPath := filepath.Join(dataDir, "server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		die(1, err.Error())
	}
	self, err := os.Executable()
	if err != nil {
		die(1, err.Error())
	}
	cmd := exec.Command(self, "serve", "--addr", addr, "--data-dir", dataDir)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		die(1, err.Error())
	}
	_ = cmd.Process.Release()
	for i := 0; i < 40; i++ {
		if _, ok := healthy(addr); ok {
			fmt.Printf("guided-pr running at http://%s (log: %s)\n", addr, logPath)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	die(1, "guided-pr did not start; see "+logPath)
}

func stop(addr string) {
	if _, ok := healthy(addr); !ok {
		fmt.Println("guided-pr is not running")
		return
	}
	req, _ := http.NewRequest("POST", "http://"+addr+"/api/shutdown", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-Guided-PR", "1")
	req.Header.Set("Content-Type", "application/json")
	if _, err := http.DefaultClient.Do(req); err != nil {
		die(1, err.Error())
	}
	for i := 0; i < 40; i++ {
		if _, ok := healthy(addr); !ok {
			fmt.Println("guided-pr stopped")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	die(1, "guided-pr did not stop")
}

func status(addr string) {
	if v, ok := healthy(addr); ok {
		fmt.Printf("guided-pr %s running at http://%s\n", v, addr)
		return
	}
	fmt.Println("guided-pr is not running")
	os.Exit(1)
}

// installSkill writes the embedded skills into the agents' skill folders.
func installSkill(claude, opencode bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		die(1, err.Error())
	}
	if !claude && !opencode {
		die(2, "choose which agent to install for:\n  guided-pr install-skill --opencode   OpenCode (skill + /guided-pr-review command)\n  guided-pr install-skill --claude     Claude Code (or use the plugin: /plugin marketplace add Gabb1995/guided-pr)\nInstall each agent only one way, or it will list the skill twice.")
	}
	if opencode {
		// OpenCode also reads ~/.claude/skills, where the Claude variant would show up as a second copy.
		if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "guided-pr-review")); err == nil && !claude {
			fmt.Println("Warning: ~/.claude/skills/guided-pr-review exists; OpenCode reads that folder too and will list the skill twice.")
			fmt.Println("If you use the Claude Code plugin, delete that folder.")
		}
	}
	write := func(path string, data []byte, agent string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			die(1, err.Error())
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			die(1, err.Error())
		}
		fmt.Printf("installed %s: %s\n", agent, path)
	}
	if claude {
		write(filepath.Join(home, ".claude", "skills", "guided-pr-review", "SKILL.md"), guidedpr.ClaudeSkill, "the Claude Code skill")
	}
	if opencode {
		write(filepath.Join(home, ".config", "opencode", "skills", "guided-pr-review", "SKILL.md"), guidedpr.OpenCodeSkill, "the OpenCode skill")
		write(filepath.Join(home, ".config", "opencode", "commands", "guided-pr-review.md"), guidedpr.OpenCodeCommand, "the OpenCode /guided-pr-review command")
	}
	fmt.Println()
	doctorQuiet()
	fmt.Println("Restart your agent, then ask it for a guided review of an MR/PR URL.")
}

// doctor checks the forge CLIs. It fails only when neither is usable, since most people need just one.
func doctor() {
	fmt.Println("guided-pr", version)
	usable := 0
	for _, t := range []forge.Tool{forge.Glab, forge.GH} {
		if err := t.Check(context.Background()); err != nil {
			fmt.Printf("  ✗ %s (%s): %v\n", t.Name, t.Forge, err)
		} else {
			fmt.Printf("  ✓ %s: ready for %s\n", t.Name, t.Forge)
			usable++
		}
	}
	if usable == 0 {
		fmt.Println("Install and log in to glab (GitLab) and/or gh (GitHub) for the forges you review on.")
		os.Exit(1)
	}
}

// doctorQuiet prints only what's missing.
func doctorQuiet() {
	for _, t := range []forge.Tool{forge.Glab, forge.GH} {
		if err := t.Check(context.Background()); err != nil {
			fmt.Printf("Note: %v (only needed for %s).\n", err, t.Forge)
		}
	}
}

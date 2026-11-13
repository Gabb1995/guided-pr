// guided-pr serves a guided, presentation-style review of a GitLab merge request or GitHub pull request,
// and gives the agent session that wrote the walkthrough a small CLI to answer the reviewer's questions.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Gabb1995/guided-pr/internal/forge"
	"github.com/Gabb1995/guided-pr/internal/session"
	"github.com/Gabb1995/guided-pr/internal/web"
)

const usage = `usage:
  guided-pr start                              start the server in the background
  guided-pr doctor                             check that glab / gh are installed and logged in
  guided-pr stop | status | version            (status with no arguments: is the server running?)
  guided-pr install-skill --claude|--opencode   install the agent skill
  guided-pr serve  [--addr 127.0.0.1:8787] [--data-dir DIR]   run in the foreground
  guided-pr new    URL                         create a session; prints {id, reviewUrl, bundleUrl}
  guided-pr bundle SESSION                     print the analysis bundle (diff pinned to the head SHA)
  guided-pr deck   SESSION FILE                import a review-deck.v1 file
  guided-pr next   SESSION [--wait 25m]        wait for the reviewer's next question and print it
  guided-pr status SESSION QUESTION TEXT       show a progress line in the browser
  guided-pr answer SESSION QUESTION FILE       answer a question with an answer JSON file

All client commands take --addr (default 127.0.0.1:8787).
next exits 0 with a question, 3 when --wait passes with no question, and 4 when the reviewer finished.`

func main() {
	if len(os.Args) < 2 {
		die(2, usage)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "serve" {
		serve(args)
		return
	}
	flags := flag.NewFlagSet(cmd, flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:8787", "server address")
	wait := flags.Duration("wait", 25*time.Minute, "next: how long to wait for a question")
	dataDir := flags.String("data-dir", defaultDataDir(), "start: session data directory")
	forClaude := flags.Bool("claude", false, "install-skill: install the Claude Code skill")
	forOpenCode := flags.Bool("opencode", false, "install-skill: install the OpenCode skill")
	// Allow flags after positional arguments.
	var positional []string
	for len(args) > 0 {
		_ = flags.Parse(args)
		args = flags.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	c := client{base: "http://" + *addr}
	need := func(n int) {
		if len(positional) != n {
			die(2, usage)
		}
	}
	switch cmd {
	case "start":
		start(*addr, *dataDir)
	case "stop":
		stop(*addr)
	case "version", "--version", "-v":
		fmt.Println("guided-pr", version)
	case "doctor":
		doctor()
	case "install-skill":
		installSkill(*forClaude, *forOpenCode)
	case "new":
		need(1)
		c.print(c.do("POST", "/api/sessions", map[string]string{"pullRequestUrl": positional[0]}))
	case "bundle":
		need(1)
		c.print(c.do("GET", "/api/sessions/"+positional[0]+"/bundle", nil))
	case "deck":
		need(2)
		c.print(c.do("PUT", "/api/sessions/"+positional[0]+"/deck", readJSONFile(positional[1])))
	case "status":
		if len(positional) == 0 {
			status(*addr)
			return
		}
		need(3)
		c.print(c.do("POST", "/api/sessions/"+positional[0]+"/questions/"+positional[1]+"/status", map[string]string{"text": positional[2]}))
	case "answer":
		need(3)
		c.print(c.do("POST", "/api/sessions/"+positional[0]+"/questions/"+positional[1]+"/answer", readJSONFile(positional[2])))
	case "next":
		need(1)
		c.next(positional[0], *wait)
	default:
		die(2, usage)
	}
}

func serve(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:8787", "local listen address")
	dataDir := flags.String("data-dir", defaultDataDir(), "session data directory")
	_ = flags.Parse(args)

	store, err := session.NewStore(*dataDir)
	if err != nil {
		die(1, err.Error())
	}
	shutdown := make(chan struct{}, 1)
	server := web.New(web.Options{Store: store, Forge: forge.Forges{GitHub: forge.GHCLI{}, GitLab: forge.GlabCLI{}}, LocalOnly: true,
		Version: version, OnShutdown: func() { shutdown <- struct{}{} }})
	// No WriteTimeout: long-polls and the event stream stay open by design.
	httpServer := &http.Server{Addr: *addr, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 120 * time.Second}
	finished := make(chan error, 1)
	go func() { finished <- httpServer.ListenAndServe() }()
	_, port, _ := net.SplitHostPort(*addr)
	fmt.Printf("guided-pr listening at http://%s (open http://localhost:%s)\n", *addr, port)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			die(1, err.Error())
		}
	case <-shutdown:
	case <-stop:
	}
	{
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}
}

type client struct{ base string }

type response struct {
	status int
	body   []byte
}

func (c client) do(method, path string, body any) response {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			die(1, err.Error())
		}
		reader = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, c.base+path, reader)
	req.Header.Set("X-Guided-PR", "1")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		die(1, "guided-pr server is not reachable at "+c.base+" (run `guided-pr start`): "+err.Error())
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return response{status: res.StatusCode, body: data}
}

func (c client) print(r response) {
	if r.status >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(r.body, &e) == nil && e.Error != "" {
			die(1, e.Error)
		}
		die(1, fmt.Sprintf("HTTP %d: %s", r.status, r.body))
	}
	if len(bytes.TrimSpace(r.body)) > 0 {
		var pretty bytes.Buffer
		if json.Indent(&pretty, r.body, "", "  ") == nil {
			fmt.Println(pretty.String())
			return
		}
		os.Stdout.Write(r.body)
	} else {
		fmt.Println("ok")
	}
}

// next long-polls in short rounds so a dropped connection costs little, until a question arrives or wait passes.
func (c client) next(id string, wait time.Duration) {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		round := time.Until(deadline)
		if round > 4*time.Minute {
			round = 4 * time.Minute
		}
		r := c.do("POST", fmt.Sprintf("/api/sessions/%s/next?wait=%d", id, int(round.Seconds())+1), map[string]string{})
		switch r.status {
		case 200:
			c.print(r)
			return
		case 204:
			continue
		case 410:
			fmt.Println(`{"ended": true}`)
			os.Exit(4)
		default:
			c.print(r)
		}
	}
	fmt.Println(`{"timeout": true}`)
	os.Exit(3)
}

func readJSONFile(path string) json.RawMessage {
	data, err := os.ReadFile(path)
	if err != nil {
		die(1, err.Error())
	}
	if !json.Valid(data) {
		die(1, path+" is not valid JSON")
	}
	return data
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".guided-pr"
	}
	return filepath.Join(home, ".local", "share", "guided-pr")
}

func die(code int, msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(code)
}

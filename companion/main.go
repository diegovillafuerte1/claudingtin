// Command companion is the claudingtin think-time signal. Launched once per
// Claude Code session by the SessionStart hook (Story 1.7) with three
// positional arguments — transcript-path, config-dir, server-url — it loads the
// anonymous account key, opens one websocket to the backend, tails the session
// transcript, and translates each turn boundary into a ready / busy frame. It
// writes only its own status line to stdout and diagnostics to stderr; it never
// touches a Claude Code stream and renders no chat UI.
//
// Exit codes: 0 clean exit (context cancelled, the peer ended the session, or
// the server asked for an update and the process was then signalled); 2 bad
// arguments; 1 any startup or runtime failure.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/diegovillafuerte1/claudingtin/companion/internal/identity"
	runner "github.com/diegovillafuerte1/claudingtin/companion/internal/run"
	"github.com/diegovillafuerte1/claudingtin/proto"
)

// defaultServerURL is the compiled-in fallback. It is a localhost placeholder
// until Epic 6 provisions the public instance; moving it is Ask-First.
const defaultServerURL = "ws://127.0.0.1:8080/ws"

// errUsage is returned by resolve for a wrong argument count. run prints it and
// returns exit code 2.
var errUsage = errors.New("usage: companion <transcript-path> <config-dir> <server-url>")

func main() {
	// Keep the proto edge real for scripts/check_deps.sh even though runner
	// already imports it transitively.
	_ = proto.PROTOCOL_VERSION

	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is main without the process exit: it does everything and returns the exit
// code, so the argument/identity/exit-code wiring is testable. os.Exit lives
// only in main.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, configDir, err := resolve(args, getenv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, errUsage) {
			return 2
		}
		return 1
	}

	if configDir == "" {
		configDir, err = identity.DefaultConfigDir()
		if err != nil {
			fmt.Fprintln(stderr, "companion: resolve config dir:", err)
			return 1
		}
	}

	key, err := identity.Load(configDir)
	if err != nil {
		// identity.Load guarantees no key material in the error.
		fmt.Fprintln(stderr, "companion: load account key:", err)
		return 1
	}
	cfg.AccountKey = key
	cfg.Out = stdout
	cfg.Err = stderr

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runner.Run(ctx, cfg); err != nil {
		fmt.Fprintln(stderr, "companion:", err)
		return 1
	}
	return 0
}

// resolve turns the positional launch arguments into a run.Config plus the
// config-dir path (kept separate because run.Config carries no config dir — the
// key is loaded in run). It is pure: the only environment it reads is the
// injected env function, so the arg-resolution table is unit-testable.
//
// Rules:
//   - exactly three args, else errUsage;
//   - a non-empty transcript-path is required;
//   - an empty server-url arg falls back to env("SERVER_URL"), then to the
//     compiled default;
//   - the resolved URL must parse, use the ws or wss scheme, and carry a host;
//   - if its path is empty or "/", "/ws" is appended;
//   - an empty config-dir is returned as "" for the caller to replace with
//     identity.DefaultConfigDir().
func resolve(args []string, env func(string) string) (runner.Config, string, error) {
	if len(args) != 3 {
		return runner.Config{}, "", errUsage
	}
	transcriptPath, configDir, rawURL := args[0], args[1], args[2]

	if transcriptPath == "" {
		return runner.Config{}, "", errors.New("transcript-path (argument 1) must not be empty")
	}

	if rawURL == "" {
		rawURL = env("SERVER_URL")
	}
	if rawURL == "" {
		rawURL = defaultServerURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		// Never echo the raw value — it may carry userinfo from $SERVER_URL.
		return runner.Config{}, "", errors.New("server-url is not a valid URL")
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return runner.Config{}, "", fmt.Errorf("server-url must use ws:// or wss:// (got scheme %q)", u.Scheme)
	}
	if u.Host == "" {
		return runner.Config{}, "", errors.New("server-url is missing a host")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	}

	return runner.Config{
		TranscriptPath: transcriptPath,
		ServerURL:      u.String(),
	}, configDir, nil
}

package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/diegovillafuerte1/claudingtin/proto"
)

// lockedBuf is a concurrency-safe io.Writer for the tests that run() in a
// goroutine while the test body inspects the captured output.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name           string
		args           []string
		env            map[string]string
		wantErr        bool
		wantUsageErr   bool
		wantURL        string
		wantConfigDir  string
		wantTranscript string
	}{
		{
			name:         "no args",
			args:         nil,
			wantErr:      true,
			wantUsageErr: true,
		},
		{
			name:         "two args",
			args:         []string{"/t", "/cfg"},
			wantErr:      true,
			wantUsageErr: true,
		},
		{
			name:         "four args",
			args:         []string{"/t", "/cfg", "ws://h/ws", "extra"},
			wantErr:      true,
			wantUsageErr: true,
		},
		{
			name:           "all three provided",
			args:           []string{"/tmp/session.jsonl", "/tmp/cfg", "ws://host:9/ws"},
			wantURL:        "ws://host:9/ws",
			wantConfigDir:  "/tmp/cfg",
			wantTranscript: "/tmp/session.jsonl",
		},
		{
			name:          "blank server-url falls back to SERVER_URL env",
			args:          []string{"/t", "/cfg", ""},
			env:           map[string]string{"SERVER_URL": "wss://env.example/ws"},
			wantURL:       "wss://env.example/ws",
			wantConfigDir: "/cfg",
		},
		{
			name:          "blank server-url and no env falls back to compiled default",
			args:          []string{"/t", "/cfg", ""},
			wantURL:       defaultServerURL,
			wantConfigDir: "/cfg",
		},
		{
			name:          "env is ignored when the server-url arg is set",
			args:          []string{"/t", "/cfg", "ws://arg/ws"},
			env:           map[string]string{"SERVER_URL": "ws://env/ws"},
			wantURL:       "ws://arg/ws",
			wantConfigDir: "/cfg",
		},
		{
			name:          "url with empty path gets /ws appended",
			args:          []string{"/t", "/cfg", "wss://host"},
			wantURL:       "wss://host/ws",
			wantConfigDir: "/cfg",
		},
		{
			name:          "url with bare slash path gets /ws appended",
			args:          []string{"/t", "/cfg", "wss://host/"},
			wantURL:       "wss://host/ws",
			wantConfigDir: "/cfg",
		},
		{
			name:          "url with a real path is left alone",
			args:          []string{"/t", "/cfg", "wss://host/socket"},
			wantURL:       "wss://host/socket",
			wantConfigDir: "/cfg",
		},
		{
			name:          "blank config-dir stays empty for the caller to default",
			args:          []string{"/t", "", "ws://h/ws"},
			wantURL:       "ws://h/ws",
			wantConfigDir: "",
		},
		{
			name:          "env fallback also gets the /ws append",
			args:          []string{"/t", "/cfg", ""},
			env:           map[string]string{"SERVER_URL": "wss://envhost"},
			wantURL:       "wss://envhost/ws",
			wantConfigDir: "/cfg",
		},
		{
			name:    "empty transcript-path is rejected",
			args:    []string{"", "/cfg", "ws://h/ws"},
			wantErr: true,
		},
		{
			name:    "server-url with no scheme is rejected",
			args:    []string{"/t", "/cfg", "127.0.0.1:8080"},
			wantErr: true,
		},
		{
			name:    "server-url with a non-ws scheme is rejected",
			args:    []string{"/t", "/cfg", "http://h/ws"},
			wantErr: true,
		},
		{
			name:    "whitespace server-url is rejected",
			args:    []string{"/t", "/cfg", " "},
			wantErr: true,
		},
		{
			name:    "server-url with no host is rejected",
			args:    []string{"/t", "/cfg", "wss://"},
			wantErr: true,
		},
		{
			name:    "SERVER_URL env with no scheme is rejected",
			args:    []string{"/t", "/cfg", ""},
			env:     map[string]string{"SERVER_URL": "example.com/ws"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := func(k string) string { return tc.env[k] }
			cfg, configDir, err := resolve(tc.args, env)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolve(%q) = nil error, want an error", tc.args)
				}
				if tc.wantUsageErr && !errors.Is(err, errUsage) {
					t.Fatalf("resolve(%q) error = %v, want errUsage", tc.args, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve(%q) unexpected error: %v", tc.args, err)
			}
			if cfg.ServerURL != tc.wantURL {
				t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, tc.wantURL)
			}
			if configDir != tc.wantConfigDir {
				t.Errorf("configDir = %q, want %q", configDir, tc.wantConfigDir)
			}
			if tc.wantTranscript != "" && cfg.TranscriptPath != tc.wantTranscript {
				t.Errorf("TranscriptPath = %q, want %q", cfg.TranscriptPath, tc.wantTranscript)
			}
			if cfg.AccountKey != "" {
				t.Errorf("AccountKey = %q, want empty (the key is loaded in main, not resolve)", cfg.AccountKey)
			}
		})
	}
}

func TestResolveIsPureOverEnv(t *testing.T) {
	// resolve must read only the injected env function — never the real process
	// environment — so the table test above is deterministic.
	calls := 0
	env := func(k string) string {
		calls++
		if k != "SERVER_URL" {
			t.Fatalf("resolve read env %q, want only SERVER_URL", k)
		}
		return ""
	}
	if _, _, err := resolve([]string{"/t", "/cfg", ""}, env); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if calls == 0 {
		t.Fatal("resolve never consulted the injected env function for a blank server-url")
	}
}

var uuidLike = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// TestRunExitCodes covers the exit-code wiring that main delegates to run().
// Each case returns before signal.NotifyContext / runner.Run, so no server or
// transcript is needed.
func TestRunExitCodes(t *testing.T) {
	noEnv := func(string) string { return "" }

	noStdin := func() io.Reader { return strings.NewReader("") }

	t.Run("wrong arg count exits 2", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"only-one-arg"}, noEnv, noStdin(), &out, &errb); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(errb.String(), "usage:") {
			t.Fatalf("stderr = %q, want a usage line", errb.String())
		}
	})

	t.Run("invalid server-url exits 1", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := run([]string{"/tmp/t", t.TempDir(), "http://nope/ws"}, noEnv, noStdin(), &out, &errb)
		if code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if errb.Len() == 0 {
			t.Fatal("want an error on stderr")
		}
	})

	t.Run("CLAUDINGTIN_SAFETY_REVIEW reprints the screen and exits 0 with no args", func(t *testing.T) {
		env := func(k string) string {
			if k == "CLAUDINGTIN_SAFETY_REVIEW" {
				return "1"
			}
			return ""
		}
		var out, errb bytes.Buffer
		// No positional args at all: the review path runs before resolve and
		// must not depend on well-formed arguments.
		code := run(nil, env, noStdin(), &out, &errb)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
		}
		if !strings.Contains(out.String(), "18 or older") {
			t.Fatalf("stdout = %q, want the first-run screen", out.String())
		}
		if errb.Len() != 0 {
			t.Fatalf("stderr = %q, want nothing", errb.String())
		}
		if uuidLike.MatchString(out.String()) {
			t.Fatalf("screen output carries key-shaped material: %q", out.String())
		}
		// Fprint, not Fprintln: Screen() already ends in "\n", so there must be
		// no trailing blank line.
		if strings.HasSuffix(out.String(), "\n\n") {
			t.Fatalf("screen output has a stray trailing blank line:\n%q", out.String())
		}
	})

	t.Run("identity.Load failure exits non-zero with no key material", func(t *testing.T) {
		// A config-dir path whose parent is a regular file: os.MkdirAll inside
		// identity.Load fails, so Load returns an error and never generates a key.
		tmp := t.TempDir()
		notADir := filepath.Join(tmp, "file")
		if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		badConfigDir := filepath.Join(notADir, "config")

		var out, errb bytes.Buffer
		code := run([]string{"/tmp/t", badConfigDir, "ws://127.0.0.1:8080/ws"}, noEnv, noStdin(), &out, &errb)
		if code == 0 {
			t.Fatalf("exit = 0, want non-zero on identity.Load failure; stderr=%q", errb.String())
		}
		if !strings.Contains(errb.String(), "account key") {
			t.Fatalf("stderr = %q, want it to name the failed step", errb.String())
		}
		if uuidLike.MatchString(errb.String()) || uuidLike.MatchString(out.String()) {
			t.Fatalf("output carries UUID-like key material: out=%q err=%q", out.String(), errb.String())
		}
	})
}

// TestRunFullAcceptWritesAck drives run() end to end on the accept path: real
// temp transcript, a live coder/websocket server, a temp config dir, and
// stdin = "yes\n". It locks the cfg.In / cfg.ConfigDir wiring — run() must
// record <configDir>/safety-ack and connect. The server returns session_ended
// right after the first frame so run() exits 0 without a real signal.
func TestRunFullAcceptWritesAck(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		// Send session_ended and close the socket right after: that pairing is
		// a takeover / shutdown, the one session_ended shape that still exits
		// run() cleanly (a socket-open session_ended is a peer leaving and
		// keeps the companion running).
		b, _ := proto.Encode(proto.SessionEnded{})
		_ = c.Write(r.Context(), websocket.MessageText, b)
		_ = c.Close(websocket.StatusNormalClosure, "")
	}))
	defer ts.Close()

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, nil, 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	cfgDir := t.TempDir()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	var out, errb lockedBuf
	done := make(chan int, 1)
	go func() {
		done <- run([]string{transcriptPath, cfgDir, wsURL},
			func(string) string { return "" }, strings.NewReader("yes\n"), &out, &errb)
	}()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("run exit = %d, want 0; stderr=%q", code, errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("run did not return; stdout=%q stderr=%q", out.String(), errb.String())
	}

	if _, err := os.Stat(filepath.Join(cfgDir, "safety-ack")); err != nil {
		t.Fatalf("safety-ack not written after a full accept: %v", err)
	}
	if !strings.Contains(out.String(), "18 or older") {
		t.Fatalf("stdout = %q, want the first-run screen", out.String())
	}
}

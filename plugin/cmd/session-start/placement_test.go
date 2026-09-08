package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// envFrom returns a getenv that answers from m and "" for anything else.
func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestMuxStrategyArgv pins the exact command line each registry strategy builds,
// so a broken flag is caught here rather than in a terminal.
func TestMuxStrategyArgv(t *testing.T) {
	const bin = "/plugin/bin/darwin-arm64/companion"
	args := []string{"/t.jsonl", "", ""}

	want := map[string][]string{
		"wezterm": {"wezterm", "cli", "split-pane", "--right", "--percent", "40", "--", bin, "/t.jsonl", "", ""},
		"zellij":  {"zellij", "action", "new-pane", "--direction", "right", "--", bin, "/t.jsonl", "", ""},
		"kitty":   {"kitty", "@", "launch", "--type=window", "--location=vsplit", "--dont-take-focus", "--cwd=current", "--", bin, "/t.jsonl", "", ""},
		"wt":      {"wt", "-w", "0", "split-pane", bin, "/t.jsonl", "", ""},
	}

	seen := map[string]bool{}
	for _, s := range muxStrategies {
		seen[s.name] = true
		got := s.argv(envFrom(nil), bin, args)
		if !reflect.DeepEqual(got, want[s.name]) {
			t.Errorf("%s argv =\n  %#v\nwant\n  %#v", s.name, got, want[s.name])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("registry is missing the %q strategy", name)
		}
	}
}

// TestMuxStrategyEnvVarsAreDistinct guards against a copy-paste that points two
// strategies at the same detector.
func TestMuxStrategyEnvVarsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, s := range muxStrategies {
		if prev, dup := seen[s.env]; dup {
			t.Fatalf("%s and %s both detect on %s", prev, s.name, s.env)
		}
		seen[s.env] = s.name
	}
}

// TestPlaceInPane covers the walk: no detector matches, a match that succeeds, a
// match that fails (fall through), and that only the first matching strategy is
// attempted.
func TestPlaceInPane(t *testing.T) {
	const bin = "/bin/companion"
	args := []string{"/t.jsonl", "", ""}

	restoreStrat, restoreRun := muxStrategies, runPlacement
	t.Cleanup(func() { muxStrategies, runPlacement = restoreStrat, restoreRun })

	var got [][]string
	arm := func(outcomes map[string]error) {
		got = nil
		runPlacement = func(_ context.Context, argv []string) error {
			got = append(got, argv)
			return outcomes[argv[0]]
		}
	}

	muxStrategies = []muxStrategy{
		{name: "alpha", env: "ALPHA", argv: func(_ func(string) string, b string, a []string) []string {
			return append([]string{"alpha-tool", b}, a...)
		}},
		{name: "beta", env: "BETA", argv: func(_ func(string) string, b string, a []string) []string {
			return append([]string{"beta-tool", b}, a...)
		}},
	}

	t.Run("no detector matches", func(t *testing.T) {
		arm(nil)
		if placeInPane(envFrom(nil), bin, args) {
			t.Fatal("placeInPane = true with no multiplexer env set")
		}
		if len(got) != 0 {
			t.Fatalf("ran a placement command anyway: %#v", got)
		}
	})

	t.Run("match succeeds", func(t *testing.T) {
		arm(nil) // nil error == the command exited 0
		if !placeInPane(envFrom(map[string]string{"BETA": "1"}), bin, args) {
			t.Fatal("placeInPane = false when the matching strategy succeeded")
		}
		if len(got) != 1 || got[0][0] != "beta-tool" {
			t.Fatalf("commands run = %#v, want one beta-tool call", got)
		}
	})

	t.Run("match fails, falls through", func(t *testing.T) {
		arm(map[string]error{"beta-tool": errors.New("no mux server")})
		if placeInPane(envFrom(map[string]string{"BETA": "1"}), bin, args) {
			t.Fatal("placeInPane = true when the strategy command failed")
		}
	})

	t.Run("only the first matching strategy is attempted", func(t *testing.T) {
		arm(map[string]error{"alpha-tool": errors.New("boom")})
		placeInPane(envFrom(map[string]string{"ALPHA": "1", "BETA": "1"}), bin, args)
		if len(got) != 1 || got[0][0] != "alpha-tool" {
			t.Fatalf("commands run = %#v, want only the alpha-tool attempt", got)
		}
	})
}

// TestRunPlacementTool: a nil argv and a missing binary are both errors, never a
// panic — the caller reads either as "fall through to the detached spawn".
func TestRunPlacementTool(t *testing.T) {
	if err := runPlacementTool(context.Background(), nil); err == nil {
		t.Fatal("runPlacementTool(nil) returned nil")
	}
	if err := runPlacementTool(context.Background(), []string{"claudingtin-no-such-mux-tool", "x"}); err == nil {
		t.Fatal("runPlacementTool on a missing binary returned nil")
	}
}

// TestLaunchTriesPlacementBeforeSpawn: with a multiplexer detected and placePane
// reporting success, launch must not fall through to the detached spawn or print
// the fallback hint.
func TestLaunchTriesPlacementBeforeSpawn(t *testing.T) {
	const stdinJSON = `{"session_id":"plc1","transcript_path":"/tmp/t.jsonl","hook_event_name":"SessionStart"}`

	fakePluginRoot(t)
	restore := placePane
	t.Cleanup(func() { placePane = restore })

	var placed [][]string
	placePane = func(_ func(string) string, b string, a []string) bool {
		placed = append(placed, append([]string{b}, a...))
		return true
	}

	rec := &recordingSpawn{}
	tmx := &recordingTmux{}
	var out bytes.Buffer
	getenv := func(k string) string {
		if k == "WEZTERM_PANE" {
			return "3"
		}
		return ""
	}

	if err := launch(strings.NewReader(stdinJSON), getenv, t.TempDir(), &out, rec.fn, tmx.fn); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if len(placed) != 1 {
		t.Fatalf("placePane calls = %d, want 1", len(placed))
	}
	if got := placed[0]; len(got) != 4 || got[1] != "/tmp/t.jsonl" || got[2] != "" || got[3] != "" {
		t.Fatalf("placePane args = %#v, want [<bin> /tmp/t.jsonl \"\" \"\"]", got)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("launch fell through to the detached spawn: %#v", rec.calls)
	}
	if out.Len() != 0 {
		t.Fatalf("launch printed the fallback hint despite a successful placement: %q", out.String())
	}
}

// TestLaunchPlacementFailureFallsBackToSpawn: placePane reporting false leaves
// the Story 1.7 detached-spawn + hint path exactly as it was.
func TestLaunchPlacementFailureFallsBackToSpawn(t *testing.T) {
	const stdinJSON = `{"session_id":"plc2","transcript_path":"/tmp/t.jsonl","hook_event_name":"SessionStart"}`

	fakePluginRoot(t)
	restore := placePane
	t.Cleanup(func() { placePane = restore })
	placePane = func(_ func(string) string, _ string, _ []string) bool { return false }

	rec := &recordingSpawn{}
	tmx := &recordingTmux{}
	var out bytes.Buffer

	if err := launch(strings.NewReader(stdinJSON), envFrom(nil), t.TempDir(), &out, rec.fn, tmx.fn); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1 (placement declined)", len(rec.calls))
	}
	if strings.TrimRight(out.String(), "\n") == "" {
		t.Fatal("no fallback hint line printed after the detached spawn")
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests validate the *shipped* plugin manifest and hook registration
// against the shape Claude Code's plugin loader actually accepts. They exist
// because Story 1.7 originally shipped hooks.json in the settings.json hook
// shape (a top-level "SessionStart" key) instead of the plugin shape (events
// nested under "hooks"), which made Claude Code reject the whole plugin with
// "hooks.json must have `hooks` (the hook matchers) or `modules`". Nothing
// caught it: there was no schema test and plugin/bin/ was empty so the plugin
// was never actually installed. See epic-1-retro / F8.
//
// Paths are relative to this package dir (plugin/cmd/session-start), which is
// where `go test` runs.
const (
	pluginJSONPath = "../../.claude-plugin/plugin.json"
	hooksJSONPath  = "../../hooks/hooks.json"
)

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func TestPluginManifestShape(t *testing.T) {
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Hooks   any    `json:"hooks"`
	}
	readJSON(t, pluginJSONPath, &m)

	if m.Name == "" {
		t.Errorf("%s: missing \"name\"", pluginJSONPath)
	}
	// The hooks/hooks.json file is auto-discovered; a "hooks" key in
	// plugin.json is redundant and, when it pointed at the same malformed
	// file, made Claude Code report the load failure twice. Keep it absent.
	if m.Hooks != nil {
		t.Errorf("%s: unexpected \"hooks\" key (%v) — hooks/hooks.json is auto-discovered; drop it", pluginJSONPath, m.Hooks)
	}
}

func TestHooksJSONIsPluginShape(t *testing.T) {
	// Decode into a struct that only exists in the *plugin* shape: the event
	// arrays live under a top-level "hooks" object. A file in the settings.json
	// shape (top-level "SessionStart") leaves Hooks.SessionStart empty and is
	// caught below.
	var doc struct {
		Description string `json:"description"`
		Hooks       struct {
			SessionStart []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	readJSON(t, hooksJSONPath, &doc)

	// Guard against a regression to the settings.json shape.
	var raw map[string]json.RawMessage
	readJSON(t, hooksJSONPath, &raw)
	if _, ok := raw["hooks"]; !ok {
		t.Fatalf("%s: no top-level \"hooks\" key — this is the settings.json shape, which Claude Code's plugin loader rejects", hooksJSONPath)
	}
	if _, ok := raw["SessionStart"]; ok {
		t.Errorf("%s: has a top-level \"SessionStart\" key — event arrays belong under \"hooks\"", hooksJSONPath)
	}

	if len(doc.Hooks.SessionStart) == 0 {
		t.Fatalf("%s: hooks.SessionStart is empty", hooksJSONPath)
	}
	entry := doc.Hooks.SessionStart[0]
	if entry.Matcher != "startup|resume" {
		t.Errorf("%s: SessionStart matcher = %q, want \"startup|resume\"", hooksJSONPath, entry.Matcher)
	}
	if len(entry.Hooks) == 0 {
		t.Fatalf("%s: SessionStart[0].hooks is empty", hooksJSONPath)
	}
	h := entry.Hooks[0]
	if h.Type != "command" {
		t.Errorf("%s: hook type = %q, want \"command\"", hooksJSONPath, h.Type)
	}
	if !strings.Contains(h.Command, "${CLAUDE_PLUGIN_ROOT}") {
		t.Errorf("%s: hook command %q does not reference ${CLAUDE_PLUGIN_ROOT}", hooksJSONPath, h.Command)
	}
	if !strings.Contains(h.Command, "hooks/session-start.sh") {
		t.Errorf("%s: hook command %q does not run hooks/session-start.sh", hooksJSONPath, h.Command)
	}
	if h.Timeout <= 0 {
		t.Errorf("%s: hook timeout = %d, want > 0", hooksJSONPath, h.Timeout)
	}
}

func TestHookCommandScriptExists(t *testing.T) {
	// The command the manifest names must actually be in the tree.
	script := filepath.Join("../..", "hooks", "session-start.sh")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat %s: %v", script, err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("%s is not executable (mode %v)", script, info.Mode().Perm())
	}
}

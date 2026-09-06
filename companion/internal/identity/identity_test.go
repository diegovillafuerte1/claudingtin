package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// uuidRe matches a canonical lowercase UUIDv4 anywhere in a string. Used to
// prove key material never lands in an error message.
var uuidRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)

// keyPath is the account-key file inside a configDir.
func keyPath(dir string) string { return filepath.Join(dir, keyFileName) }

// perm returns the file's permission bits. Permission assertions that call this
// are skipped on Windows, where Unix mode bits are not meaningful.
func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

func mustLoad(t *testing.T, dir string) string {
	t.Helper()
	key, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", dir, err)
	}
	if !looksLikeUUIDv4(key) {
		t.Fatalf("Load returned %q, not a canonical UUIDv4", key)
	}
	return key
}

// --- First run ------------------------------------------------------------

func TestLoadFirstRun(t *testing.T) {
	cases := []struct {
		name       string
		configDirF func(base string) string // maps a fresh temp dir to the configDir passed to Load
		created    bool                     // Load has to MkdirAll it -> mode 0700 asserted
	}{
		{"dir exists", func(base string) string { return base }, false},
		{"dir absent", func(base string) string { return filepath.Join(base, "nested", "cfg") }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := c.configDirF(t.TempDir())
			key := mustLoad(t, dir)

			if runtime.GOOS != "windows" {
				if got := perm(t, keyPath(dir)); got != keyFileMode {
					t.Errorf("key file mode = %o, want %o", got, keyFileMode)
				}
				if c.created {
					if got := perm(t, dir); got != configDirMode {
						t.Errorf("config dir mode = %o, want %o", got, configDirMode)
					}
				}
			}
			// The returned value is exactly what is on disk, newline-terminated.
			raw, err := os.ReadFile(keyPath(dir))
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if strings.TrimSpace(string(raw)) != key {
				t.Errorf("on-disk %q != returned %q", strings.TrimSpace(string(raw)), key)
			}
			if !strings.HasSuffix(string(raw), "\n") {
				t.Errorf("fresh key file %q does not end with a newline", raw)
			}
		})
	}
}

// --- Reuse --------------------------------------------------------------

func TestLoadReuseLeavesFileUntouched(t *testing.T) {
	valid := "3f2504e0-4f89-4d0b-9d0a-0242ac130003"
	for _, trailing := range []string{"", "\n", "  \n\t", "\r\n"} {
		t.Run("trailing="+strings.NewReplacer("\n", `\n`, "\t", `\t`, "\r", `\r`).Replace(trailing), func(t *testing.T) {
			dir := t.TempDir()
			content := []byte(valid + trailing)
			if err := os.WriteFile(keyPath(dir), content, 0o600); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(keyPath(dir))

			key, err := Load(dir)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if key != valid {
				t.Errorf("key = %q, want %q (trimmed)", key, valid)
			}
			after, _ := os.ReadFile(keyPath(dir))
			if string(before) != string(after) {
				t.Errorf("file bytes changed: %q -> %q", before, after)
			}
		})
	}
}

// --- Reinstall stability (Acceptance Criterion) -----------------------------

func TestLoadSurvivesPluginDirDeletion(t *testing.T) {
	configDir := t.TempDir()

	pluginDir := filepath.Join(t.TempDir(), "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "companion"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	key1 := mustLoad(t, configDir)

	// Simulate a reinstall: the plugin directory is wiped and recreated; the
	// config dir (a different path) is left alone.
	if err := os.RemoveAll(pluginDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}

	key2 := mustLoad(t, configDir)

	if key1 != key2 {
		t.Fatalf("key changed across reinstall: %q -> %q", key1, key2)
	}
	if runtime.GOOS != "windows" {
		if got := perm(t, keyPath(configDir)); got != keyFileMode {
			t.Errorf("key file mode = %o, want %o", got, keyFileMode)
		}
	}
}

// --- Empty or corrupt -> regenerated --------------------------------------

func TestLoadRegeneratesEmptyOrCorrupt(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"zero bytes", ""},
		{"whitespace only", "   \n\t  "},
		{"garbage", "not-a-uuid-at-all"},
		{"truncated", "3f2504e0-4f89-4d0b-9d0a"},
		{"wrong version", "3f2504e0-4f89-1d0b-9d0a-0242ac130003"},
		{"wrong variant", "3f2504e0-4f89-4d0b-cd0a-0242ac130003"},
		{"uppercase hex", "3F2504E0-4F89-4D0B-9D0A-0242AC130003"},
		{"trailing junk", "3f2504e0-4f89-4d0b-9d0a-0242ac130003xx"},
		{"leading junk", "xx3f2504e0-4f89-4d0b-9d0a-0242ac130003"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(keyPath(dir), []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}

			key := mustLoad(t, dir)
			if strings.TrimSpace(c.content) == key {
				t.Fatalf("returned the corrupt content verbatim: %q", key)
			}
			raw, err := os.ReadFile(keyPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(raw)) != key {
				t.Errorf("on-disk %q != returned %q", strings.TrimSpace(string(raw)), key)
			}
			if !strings.HasSuffix(string(raw), "\n") {
				t.Errorf("regenerated key file %q does not end with a newline", raw)
			}
			if leftovers, _ := filepath.Glob(filepath.Join(dir, "."+keyFileName+"-*")); len(leftovers) != 0 {
				t.Errorf("temp files left behind after regenerate: %v", leftovers)
			}
			if runtime.GOOS != "windows" {
				if got := perm(t, keyPath(dir)); got != keyFileMode {
					t.Errorf("regenerated key file mode = %o, want %o", got, keyFileMode)
				}
			}
			// Second call now sees a valid key and returns it unchanged.
			key2 := mustLoad(t, dir)
			if key2 != key {
				t.Errorf("second Load changed the key: %q -> %q", key, key2)
			}
		})
	}
}

// --- Broader perms tightened --------------------------------------------

func TestLoadTightensBroaderPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits not meaningful on Windows")
	}
	dir := t.TempDir()
	valid := "3f2504e0-4f89-4d0b-9d0a-0242ac130003"
	if err := os.WriteFile(keyPath(dir), []byte(valid+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	key, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if key != valid {
		t.Errorf("key = %q, want %q", key, valid)
	}
	if got := perm(t, keyPath(dir)); got != keyFileMode {
		t.Errorf("perms not tightened: mode = %o, want %o", got, keyFileMode)
	}
}

// A tighter-than-0600 valid file is left as-is (never loosened).
func TestLoadDoesNotLoosenTightPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits not meaningful on Windows")
	}
	dir := t.TempDir()
	valid := "3f2504e0-4f89-4d0b-9d0a-0242ac130003"
	if err := os.WriteFile(keyPath(dir), []byte(valid+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := perm(t, keyPath(dir)); got != 0o400 {
		t.Errorf("tight perms changed: mode = %o, want %o", got, 0o400)
	}
}

// --- Concurrent first run (race) ---------------------------------------

func TestLoadConcurrentFirstRunConverges(t *testing.T) {
	dir := t.TempDir()
	const n = 16

	var wg sync.WaitGroup
	keys := make([]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			keys[i], errs[i] = Load(dir)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: Load error: %v", i, errs[i])
		}
		if !looksLikeUUIDv4(keys[i]) {
			t.Fatalf("goroutine %d: %q is not a canonical UUIDv4", i, keys[i])
		}
	}
	for i := 1; i < n; i++ {
		if keys[i] != keys[0] {
			t.Fatalf("callers diverged: keys[0]=%q keys[%d]=%q", keys[0], i, keys[i])
		}
	}
	raw, err := os.ReadFile(keyPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != keys[0] {
		t.Errorf("on-disk %q != converged key %q", strings.TrimSpace(string(raw)), keys[0])
	}
}

// --- Unwritable configDir --------------------------------------------

func TestLoadUnwritableConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0500 does not block writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permission")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	key, err := Load(dir)
	if err == nil {
		t.Fatalf("expected an error, got key %q", key)
	}
	if key != "" {
		t.Errorf("key returned alongside error: %q", key)
	}
	if !strings.Contains(err.Error(), "identity:") {
		t.Errorf("error %q does not name the package/op", err)
	}
	if _, statErr := os.Stat(keyPath(dir)); !os.IsNotExist(statErr) {
		t.Errorf("a partial account-key file was left behind (stat err: %v)", statErr)
	}
}

// --- Log/err scrub ----------------------------------------------------

func TestErrorsNeverContainKeyMaterial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs chmod to force a regenerate failure")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permission")
	}
	dir := t.TempDir()
	existing := "3f2504e0-4f89-4d0b-9d0a-0242ac130003"

	// First a clean load so we have a concrete key that must not leak.
	if err := os.WriteFile(keyPath(dir), []byte(existing+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := Load(dir)
	if err != nil || key != existing {
		t.Fatalf("setup Load: key=%q err=%v", key, err)
	}

	// Now corrupt the file and make the dir unwritable so regeneration fails.
	if err := os.WriteFile(keyPath(dir), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err = Load(dir)
	if err == nil {
		t.Fatal("expected a regenerate failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "identity:") {
		t.Errorf("error %q does not name the package/op", msg)
	}
	if strings.Contains(msg, key) {
		t.Errorf("error leaked the existing key: %q", msg)
	}
	if uuidRe.MatchString(msg) {
		t.Errorf("error contains a UUID-shaped substring: %q", msg)
	}
}

// --- DefaultConfigDir ------------------------------------------------

func TestDefaultConfigDir(t *testing.T) {
	got, err := DefaultConfigDir()
	if err != nil {
		t.Fatalf("DefaultConfigDir: %v", err)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("os.UserConfigDir unavailable on this host: %v", err)
	}
	want := filepath.Join(base, "claudingtin")
	if got != want {
		t.Errorf("DefaultConfigDir() = %q, want %q", got, want)
	}
	if filepath.Base(got) != "claudingtin" {
		t.Errorf("DefaultConfigDir base = %q, want claudingtin", filepath.Base(got))
	}
}

// --- newUUIDv4 / looksLikeUUIDv4 units --------------------------------

func TestNewUUIDv4(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 2000; i++ {
		u, err := newUUIDv4()
		if err != nil {
			t.Fatalf("newUUIDv4: %v", err)
		}
		if !looksLikeUUIDv4(u) {
			t.Fatalf("newUUIDv4 produced non-canonical %q", u)
		}
		if u[14] != '4' {
			t.Fatalf("version char = %c, want 4 (%q)", u[14], u)
		}
		if c := u[19]; c != '8' && c != '9' && c != 'a' && c != 'b' {
			t.Fatalf("variant char = %c, want one of 8/9/a/b (%q)", c, u)
		}
		if seen[u] {
			t.Fatalf("duplicate UUID generated: %q", u)
		}
		seen[u] = true
	}
}

func TestLooksLikeUUIDv4(t *testing.T) {
	good := []string{
		"3f2504e0-4f89-4d0b-9d0a-0242ac130003",
		"00000000-0000-4000-8000-000000000000",
		"ffffffff-ffff-4fff-bfff-ffffffffffff",
	}
	for _, s := range good {
		if !looksLikeUUIDv4(s) {
			t.Errorf("looksLikeUUIDv4(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",
		"   ",
		"3f2504e0-4f89-4d0b-9d0a-0242ac13000",   // 35
		"3f2504e0-4f89-4d0b-9d0a-0242ac1300033", // 37
		"3f2504e0-4f89-4d0b-9d0a-0242ac13000g",  // non-hex
		"3F2504E0-4F89-4D0B-9D0A-0242AC130003",  // uppercase
		"3f2504e04f894d0b9d0a0242ac130003",      // no dashes
		"3f2504e0_4f89_4d0b_9d0a_0242ac130003",  // wrong separators
		"3f2504e0-4f89-1d0b-9d0a-0242ac130003",  // version 1
		"3f2504e0-4f89-4d0b-0d0a-0242ac130003",  // variant 0
		"3f2504e0-4f89-4d0b-cd0a-0242ac130003",  // variant c
	}
	for _, s := range bad {
		if looksLikeUUIDv4(s) {
			t.Errorf("looksLikeUUIDv4(%q) = true, want false", s)
		}
	}
}

// --- DefaultConfigDir failure path (matrix row 9) -------------------------

func TestDefaultConfigDirError(t *testing.T) {
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", "")
	case "darwin", "ios":
		t.Setenv("HOME", "")
	default:
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "")
	}
	got, err := DefaultConfigDir()
	if err == nil {
		t.Fatalf("DefaultConfigDir() = %q, want an error when the OS config dir cannot be resolved", got)
	}
	if got != "" {
		t.Errorf("DefaultConfigDir() returned %q alongside its error", got)
	}
	if !strings.Contains(err.Error(), "identity:") {
		t.Errorf("error %q does not name the package", err)
	}
}

// --- MkdirAll failure names the op (matrix rows 1/7) --------------------

func TestLoadMkdirAllFails(t *testing.T) {
	base := t.TempDir()
	notADir := filepath.Join(base, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// configDir's parent path component is a regular file → MkdirAll fails.
	configDir := filepath.Join(notADir, "cfg")

	key, err := Load(configDir)
	if err == nil {
		t.Fatalf("expected an error, got key %q", key)
	}
	if key != "" {
		t.Errorf("key returned alongside error: %q", key)
	}
	if !strings.Contains(err.Error(), "identity: create config dir:") {
		t.Errorf("error %q is missing the 'create config dir' wrap", err)
	}
}

// --- resolveExisting reads the winner's key via its retry loop (matrix row 6)

func TestResolveExistingWaitsForWinner(t *testing.T) {
	// Widen the convergence window so a slow scheduler cannot make this flake;
	// the winner writes within ~15ms regardless.
	origAttempts := convergeAttempts
	convergeAttempts = 400 // 400 * 5ms = 2s ceiling
	t.Cleanup(func() { convergeAttempts = origAttempts })

	dir := t.TempDir()
	path := keyPath(dir)
	// A zero-byte account-key already exists: exactly the state an O_EXCL winner
	// leaves between its create and its write.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	want := "3f2504e0-4f89-4d0b-9d0a-0242ac130003"

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(15 * time.Millisecond)
		tmp := path + ".winner"
		if err := os.WriteFile(tmp, []byte(want+"\n"), 0o600); err != nil {
			return
		}
		_ = os.Rename(tmp, path)
	}()

	got, err := resolveExisting(path)
	<-done
	if err != nil {
		t.Fatalf("resolveExisting: %v", err)
	}
	if got != want {
		t.Fatalf("resolveExisting = %q, want the winner's key %q read via the retry loop (not a fresh regenerate)", got, want)
	}
	if raw, _ := os.ReadFile(path); strings.TrimSpace(string(raw)) != want {
		t.Errorf("on-disk key = %q, want the winner's %q", strings.TrimSpace(string(raw)), want)
	}
}

// --- Unreadable key file → wrapped error, no regenerate, no panic --------

func TestLoadUnreadableKeyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0000 does not block reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores file read permission")
	}
	dir := t.TempDir()
	valid := "3f2504e0-4f89-4d0b-9d0a-0242ac130003"
	if err := os.WriteFile(keyPath(dir), []byte(valid+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath(dir), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyPath(dir), 0o600) })

	key, err := Load(dir)
	if err == nil {
		t.Fatalf("expected an error for an unreadable key file, got %q", key)
	}
	if key != "" {
		t.Errorf("key returned alongside error: %q", key)
	}
	if !strings.Contains(err.Error(), "identity:") {
		t.Errorf("error %q is not wrapped", err)
	}
	// It must not have been replaced.
	if raw, rerr := os.ReadFile(keyPath(dir)); rerr == nil && strings.TrimSpace(string(raw)) != valid {
		t.Errorf("unreadable key file was overwritten: on-disk %q", strings.TrimSpace(string(raw)))
	}
}

// --- Oversized key file is corrupt → regenerated (pairs with the read cap)

func TestLoadRegeneratesOversizedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(keyPath(dir), bytes.Repeat([]byte("a"), 2048), 0o600); err != nil {
		t.Fatal(err)
	}

	key := mustLoad(t, dir)

	raw, err := os.ReadFile(keyPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != key {
		t.Errorf("on-disk %q != returned %q", strings.TrimSpace(string(raw)), key)
	}
	if len(raw) > 64 {
		t.Errorf("oversized file was not replaced with a small canonical key: %d bytes on disk", len(raw))
	}
}

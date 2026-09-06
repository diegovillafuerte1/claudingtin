package fsretry

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Open and Rename must behave exactly like the os functions on the happy path
// and on a missing file, on every platform.
func TestOpenAndRenamePassThrough(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	dst := filepath.Join(dir, "b")
	if err := os.WriteFile(src, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Open(src)
	if err != nil {
		t.Fatalf("Open(existing): %v", err)
	}
	f.Close()

	if _, err := Open(filepath.Join(dir, "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open(missing) = %v, want ErrNotExist", err)
	}

	if err := Rename(src, dst); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("renamed file not at dst: %v", err)
	}

	if err := Rename(filepath.Join(dir, "nope"), dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Rename(missing) = %v, want ErrNotExist", err)
	}
}

// A reader hammering Open while a writer repeatedly renames a fresh file over
// the same path must never surface an error — this is the interleaving that
// flakes a bare os.Open on Windows and is a no-op elsewhere.
func TestOpenSurvivesConcurrentRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.WriteFile(path, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			tmp := path + ".new"
			if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
				continue
			}
			_ = Rename(tmp, path)
		}
	}()

	for i := 0; i < 400; i++ {
		f, err := Open(path)
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("Open raced with Rename: %v", err)
		}
		f.Close()
	}
	close(stop)
	wg.Wait()
}

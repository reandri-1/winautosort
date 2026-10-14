//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSingleInstance(t *testing.T) {
	key := instanceKey(t.TempDir())
	rel1, already, err := acquireSingleInstance(key)
	if err != nil || already {
		t.Fatalf("first acquire: already=%v err=%v", already, err)
	}
	rel2, already, err := acquireSingleInstance(key)
	if err != nil || !already {
		t.Fatalf("second acquire must report already running: already=%v err=%v", already, err)
	}
	rel2()
	// A different folder is a different instance.
	rel3, already, err := acquireSingleInstance(instanceKey(t.TempDir()))
	if err != nil || already {
		t.Fatalf("other folder must be allowed: already=%v err=%v", already, err)
	}
	rel3()
	// After the owner exits (handle closed) the lock is free again.
	rel1()
	rel4, already, err := acquireSingleInstance(key)
	if err != nil || already {
		t.Fatalf("lock should be free after release: already=%v err=%v", already, err)
	}
	rel4()
}

func TestInstanceKeyCaseInsensitive(t *testing.T) {
	d := t.TempDir()
	if instanceKey(d) != instanceKey(strings.ToUpper(d)) || instanceKey(d) != instanceKey(d+`\`) {
		t.Fatal("key should ignore case and trailing separator")
	}
}

func TestRenameNoReplace(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	write(t, a, "A")
	write(t, b, "B")
	if err := renameNoReplace(a, b); !errors.Is(err, os.ErrExist) {
		t.Fatalf("must refuse to overwrite, got %v", err)
	}
	if got, _ := os.ReadFile(b); string(got) != "B" {
		t.Fatal("existing destination was modified")
	}
	if !exists(a) {
		t.Fatal("source must remain after refused move")
	}
	c := filepath.Join(dir, "c")
	if err := renameNoReplace(a, c); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(c); string(got) != "A" {
		t.Fatal("content lost")
	}
}

func TestFixLongPath(t *testing.T) {
	short := `C:\a\b.txt`
	if fixLongPath(short) != short {
		t.Fatal("short path must be unchanged")
	}
	long := `C:\` + strings.Repeat(`x\`, 200) + `f.txt`
	if got := fixLongPath(long); !strings.HasPrefix(got, `\\?\`) {
		t.Fatalf("long path not prefixed: %q", got[:10])
	}
}

func TestMoveFileLongPath(t *testing.T) {
	root := t.TempDir()
	deep := root
	for len(deep) < 300 {
		deep = filepath.Join(deep, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Skipf("cannot create long path here: %v", err)
	}
	src := filepath.Join(root, "f.txt")
	write(t, src, "long")
	dest, err := MoveToDir(src, deep)
	if err != nil {
		t.Fatalf("move into >260 char path failed: %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "long" {
		t.Fatal("content mismatch")
	}
}

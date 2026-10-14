package main

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUniquePath(t *testing.T) {
	dir := t.TempDir()
	if got := ResolveUniquePath(dir, "a.pdf"); got != filepath.Join(dir, "a.pdf") {
		t.Fatalf("free name: %q", got)
	}
	write(t, filepath.Join(dir, "a.pdf"), "x")
	if got := ResolveUniquePath(dir, "a.pdf"); got != filepath.Join(dir, "a (1).pdf") {
		t.Fatalf("first collision: %q", got)
	}
	write(t, filepath.Join(dir, "a (1).pdf"), "x")
	if got := ResolveUniquePath(dir, "a.pdf"); got != filepath.Join(dir, "a (2).pdf") {
		t.Fatalf("second collision: %q", got)
	}
}

func TestMoveToDirCreatesFolderAndNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "pic.png")
	write(t, src, "one")
	dest, err := MoveToDir(src, filepath.Join(root, "images", "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(root, "images", "nested", "pic.png") {
		t.Fatalf("dest: %q", dest)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source should be gone")
	}

	write(t, src, "two")
	dest2, err := MoveToDir(src, filepath.Join(root, "images", "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest2) != "pic (1).png" {
		t.Fatalf("collision dest: %q", dest2)
	}
	if b, _ := os.ReadFile(dest); string(b) != "one" {
		t.Fatal("original was overwritten")
	}
	if b, _ := os.ReadFile(dest2); string(b) != "two" {
		t.Fatal("second content wrong")
	}
}

func TestMoveFileRefusesExistingDest(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	write(t, a, "1")
	write(t, b, "2")
	if err := MoveFile(a, b); err == nil {
		t.Fatal("expected error")
	}
}

func TestCopyThenRemove(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "s.bin"), filepath.Join(dir, "d.bin")
	write(t, src, "payload")
	if err := copyThenRemove(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "payload" {
		t.Fatal("content mismatch")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source should be removed")
	}
}

func TestMoveToDir_ReplacesZeroBytePlaceholder(t *testing.T) {
	root := t.TempDir()
	destDir := filepath.Join(root, "audio_download")
	_ = os.MkdirAll(destDir, 0o755)

	// Simulate a 0-byte dummy file already in destination
	dummy := filepath.Join(destDir, "song.mp3")
	write(t, dummy, "") // 0 bytes

	// Incoming real file with content
	src := filepath.Join(root, "song.mp3")
	write(t, src, "real audio data content")

	dest, err := MoveToDir(src, destDir)
	if err != nil {
		t.Fatalf("MoveToDir failed: %v", err)
	}

	// Must take the clean name "song.mp3", NOT "song (1).mp3"
	if dest != dummy {
		t.Fatalf("expected dest %q, got %q", dummy, dest)
	}

	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(content) != "real audio data content" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForFileUnlock_FreeFileReturnsImmediately(t *testing.T) {
	p := filepath.Join(t.TempDir(), "free.bin")
	write(t, p, "data")
	start := time.Now()
	if err := WaitForFileUnlock(context.Background(), p, 2*time.Second, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Fatalf("fast path too slow: %v", d)
	}
}

func TestWaitForFileUnlock_LockedTimesOut(t *testing.T) {
	p := filepath.Join(t.TempDir(), "busy.bin")
	f, err := os.Create(p) // handle stays open, like a browser mid-download
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = WaitForFileUnlock(context.Background(), p, 300*time.Millisecond, 50*time.Millisecond)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("want ErrLockTimeout, got %v", err)
	}
}

func TestWaitForFileUnlock_ReleasedWhileWaiting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "release.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(400 * time.Millisecond)
		f.Close()
	}()
	if err := WaitForFileUnlock(context.Background(), p, 5*time.Second, 50*time.Millisecond); err != nil {
		t.Fatalf("expected success after release, got %v", err)
	}
}

func TestWaitForFileUnlock_MissingFile(t *testing.T) {
	err := WaitForFileUnlock(context.Background(), filepath.Join(t.TempDir(), "nope"), time.Second, 50*time.Millisecond)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestWaitForFileUnlock_ReadOnlyFileIsNotLocked(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ro.bin")
	write(t, p, "data")
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := WaitForFileUnlock(context.Background(), p, time.Second, 50*time.Millisecond); err != nil {
		t.Fatalf("read-only file should count as free, got %v", err)
	}
}

func TestHasDownloadCompanion(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "music.mp3")
	write(t, target, "") // 0-byte placeholder

	if hasDownloadCompanion(target) {
		t.Fatal("should not have companion initially")
	}

	// 1. Simulate Firefox active download (.part)
	part := filepath.Join(dir, "music.mp3.part")
	write(t, part, "streaming bytes")
	if !hasDownloadCompanion(target) {
		t.Fatal("expected companion detected for music.mp3.part")
	}

	// 2. Remove .part, simulate Chrome (.crdownload)
	_ = os.Remove(part)
	cr := filepath.Join(dir, "music.mp3.crdownload")
	write(t, cr, "streaming bytes")
	if !hasDownloadCompanion(target) {
		t.Fatal("expected companion detected for music.mp3.crdownload")
	}

	// 3. Remove .crdownload
	_ = os.Remove(cr)
	if hasDownloadCompanion(target) {
		t.Fatal("companion should be gone")
	}
}

func TestCheckFileReady_ZeroByteWithCompanionNotReady(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "clip.mp4")
	write(t, target, "") // 0 bytes

	part := filepath.Join(dir, "clip.mp4.part")
	write(t, part, "data")

	ready, err := CheckFileReady(target)
	if err != nil {
		t.Fatalf("CheckFileReady err: %v", err)
	}
	if ready {
		t.Fatal("target should not be ready while .part exists")
	}

	// Once .part is removed, simulate completed download
	_ = os.Remove(part)
	write(t, target, "completed 10mb video")

	ready, err = CheckFileReady(target)
	if err != nil {
		t.Fatalf("CheckFileReady after complete err: %v", err)
	}
	if !ready {
		t.Fatal("target should be ready after download finishes")
	}
}

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

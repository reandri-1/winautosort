//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Win32 error codes meaning "someone else still holds this file".
const (
	errAccessDenied     = syscall.Errno(5)
	errSharingViolation = syscall.Errno(32)
	errLockViolation    = syscall.Errno(33)
)

// ErrLockTimeout is returned when a file stays locked/growing past the timeout.
var ErrLockTimeout = errors.New("timed out waiting for file to be released")

// tryExclusiveOpen opens path with share mode 0. This fails with a sharing violation
// whenever any other process still has the file open (e.g. a browser mid-download).
func tryExclusiveOpen(path string, access uint32) error {
	p, err := syscall.UTF16PtrFromString(fixLongPath(path))
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(p, access, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	return syscall.CloseHandle(h)
}

// isLocked reports whether another process currently holds path open.
// A missing file is returned as an error wrapping os.ErrNotExist.
func isLocked(path string) (bool, error) {
	err := tryExclusiveOpen(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case errSharingViolation, errLockViolation:
			return true, nil
		case errAccessDenied:
			// Read-only files deny write access permanently; probe with read access
			// only, so they are not mistaken for "still downloading".
			if rerr := tryExclusiveOpen(path, syscall.GENERIC_READ); rerr == nil {
				return false, nil
			}
			return true, nil
		}
	}
	return false, fmt.Errorf("probe %s: %w", path, err)
}

// hasDownloadCompanion reports whether path appears to be associated with an in-progress
// download (e.g. Firefox's .part, Chrome's .crdownload, Opera's .opdownload, IDM, etc.).
func hasDownloadCompanion(path string) bool {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	// 1. Direct companion check by appending/replacing temporary download suffixes.
	suffixes := []string{
		".part",
		".crdownload",
		".download",
		".opdownload",
		".tmp",
		".temp",
		".partial",
		".filepart",
		".aria2",
	}
	for _, s := range suffixes {
		if pathExists(path + s) {
			return true
		}
		if ext != "" && pathExists(filepath.Join(dir, stem+s)) {
			return true
		}
	}

	// 2. If the file is 0 bytes, check if ANY active download artifact exists in the same folder.
	// Browsers occasionally use "Unconfirmed <hash>.crdownload" or random temp names.
	// An empty 0-byte file must never be snatched while any download is actively in-flight.
	info, err := os.Stat(path)
	if err == nil && info.Size() == 0 {
		entries, rerr := os.ReadDir(dir)
		if rerr == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := strings.ToLower(e.Name())
				for _, s := range suffixes {
					if strings.HasSuffix(name, s) {
						return true
					}
				}
			}
		}
	}

	return false
}

// CheckFileReady performs one non-blocking readiness probe. It returns (true, nil)
// when no other process holds the file and its size is stable across the probe,
// (false, nil) when the file is still busy, and an error when it cannot be probed
// (including a wrapped os.ErrNotExist if it vanished).
func CheckFileReady(path string) (bool, error) {
	before, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	// 1. If an active download companion exists (e.g. Firefox's .part or Chrome's .crdownload),
	// the file is definitely still being downloaded.
	if hasDownloadCompanion(path) {
		return false, nil
	}

	// 2. If the file is 0 bytes:
	// Empty files during downloads (Firefox, Chrome placeholders) are not ready.
	// Require a settling period (at least 2 seconds) so creation/writing has a chance to start.
	if before.Size() == 0 {
		if time.Since(before.ModTime()) < 2*time.Second {
			return false, nil
		}
	}

	locked, err := isLocked(path)
	if err != nil || locked {
		return false, err
	}
	// Guard against writers that don't hold an exclusive lock.
	after, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return after.Size() == before.Size(), nil
}

// WaitForFileUnlock blocks until path is ready, ctx is cancelled, or timeout elapses.
// The fast path (file already free) returns immediately with no sleep.
func WaitForFileUnlock(ctx context.Context, path string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ready, err := CheckFileReady(path)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrLockTimeout
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

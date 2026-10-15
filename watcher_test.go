//go:build windows

package main

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// startWatcher launches a watcher on a fresh temp dir and returns it with a stop func.
func startWatcher(t *testing.T, mutate func(*Config)) (*Watcher, string, func()) {
	t.Helper()
	dir := t.TempDir()
	cfg := &Config{
		WatchDirectory:    dir,
		Rules:             []Rule{{Category: "image_download", Extensions: []string{".png"}}},
		DefaultFallback:   "file_download",
		ExcludeExtensions: []string{".crdownload"},
	}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.prepare(); err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	return w, dir, func() {}
}

func run(t *testing.T, w *Watcher) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	return func() { cancel(); <-done }
}

func TestWatcherEndToEnd(t *testing.T) {
	w, dir, _ := startWatcher(t, nil)
	defer run(t, w)()

	// 1. Plain routing by extension.
	write(t, filepath.Join(dir, "a.png"), "1")
	waitFor(t, "a.png routed", func() bool { return exists(filepath.Join(dir, "image_download", "a.png")) })

	// 2. Unknown extension -> fallback.
	write(t, filepath.Join(dir, "c.xyz"), "1")
	waitFor(t, "c.xyz fallback", func() bool { return exists(filepath.Join(dir, "file_download", "c.xyz")) })

	// 3. File without any extension -> fallback.
	write(t, filepath.Join(dir, "README"), "1")
	waitFor(t, "no-extension fallback", func() bool { return exists(filepath.Join(dir, "file_download", "README")) })

	// 4. Name collision never overwrites.
	write(t, filepath.Join(dir, "a.png"), "2")
	waitFor(t, "a (1).png", func() bool { return exists(filepath.Join(dir, "image_download", "a (1).png")) })
	if b, _ := os.ReadFile(filepath.Join(dir, "image_download", "a.png")); string(b) != "1" {
		t.Fatal("original a.png was overwritten")
	}

	// 5. Browser flow: .crdownload ignored while writing, routed after rename.
	part := filepath.Join(dir, "d.png.crdownload")
	f, err := os.Create(part)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("partial")
	time.Sleep(300 * time.Millisecond)
	if !exists(part) {
		t.Fatal(".crdownload must not be touched")
	}
	f.Close()
	if err := os.Rename(part, filepath.Join(dir, "d.png")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "d.png routed after rename", func() bool { return exists(filepath.Join(dir, "image_download", "d.png")) })

	// 6. A file still held open is not moved until it is released.
	busy := filepath.Join(dir, "busy.png")
	bf, err := os.Create(busy)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if !exists(busy) {
		t.Fatal("locked file was moved too early")
	}
	bf.Close()
	waitFor(t, "busy.png routed after release", func() bool { return exists(filepath.Join(dir, "image_download", "busy.png")) })

	// 7. Office owner-lock files are never touched.
	write(t, filepath.Join(dir, "~$report.docx"), "lock")
	time.Sleep(300 * time.Millisecond)
	if !exists(filepath.Join(dir, "~$report.docx")) {
		t.Fatal("~$ file must be ignored")
	}
}

func TestWatcherSurvivesPanic(t *testing.T) {
	w, dir, _ := startWatcher(t, nil)
	orig := w.handle
	w.handle = func(ctx context.Context, j job) bool {
		if filepath.Base(j.path) == "boom.png" {
			panic("simulated bug")
		}
		return orig(ctx, j)
	}
	defer run(t, w)()

	write(t, filepath.Join(dir, "boom.png"), "x")
	time.Sleep(300 * time.Millisecond)
	write(t, filepath.Join(dir, "ok.png"), "x")
	waitFor(t, "ok.png routed after a panic on another file", func() bool {
		return exists(filepath.Join(dir, "image_download", "ok.png"))
	})
	if !exists(filepath.Join(dir, "boom.png")) {
		t.Fatal("the panicking file must be left in place")
	}
}

func TestWatcherManyFilesBoundedGoroutines(t *testing.T) {
	w, dir, _ := startWatcher(t, nil)
	defer run(t, w)()

	const n = 400
	for i := 0; i < n; i++ {
		write(t, filepath.Join(dir, "f"+strconv.Itoa(i)+".png"), "x")
	}
	if g := runtime.NumGoroutine(); g > 150 {
		t.Fatalf("goroutines exploded: %d for %d files", g, n)
	}
	waitFor(t, "all files routed", func() bool {
		entries, _ := os.ReadDir(filepath.Join(dir, "image_download"))
		return len(entries) == n
	})
	left, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	if len(left) != 0 {
		t.Fatalf("%d files left behind", len(left))
	}
}

func TestSweepOnStartAndGiveUp(t *testing.T) {
	sweepTrue := true
	w, dir, _ := startWatcher(t, func(c *Config) { c.SweepOnStart = &sweepTrue; c.LockTimeoutSeconds = 1 })
	write(t, filepath.Join(dir, "old.png"), "x")
	locked := filepath.Join(dir, "locked.png")
	f, err := os.Create(locked)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	defer run(t, w)()

	waitFor(t, "pre-existing file swept", func() bool { return exists(filepath.Join(dir, "image_download", "old.png")) })
	// Past the 1s timeout the file is abandoned (not deleted, not moved) and the
	// in-flight entry is released so a later change can retry it.
	waitFor(t, "inflight released after give-up", func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return len(w.inflight) == 0
	})
	if !exists(locked) {
		t.Fatal("abandoned file must stay in place")
	}
}

func TestBackoffBounded(t *testing.T) {
	prev := time.Duration(0)
	for a := 1; a < 40; a++ {
		d := backoff(a)
		if d < prev || d > retryMax {
			t.Fatalf("attempt %d: %v (prev %v)", a, d, prev)
		}
		prev = d
	}
	if backoff(1) != retryBase || backoff(40) != retryMax {
		t.Fatal("unexpected bounds")
	}
}

func TestIsIgnoredName(t *testing.T) {
	for _, n := range []string{
		"desktop.ini", "Thumbs.db", "~$x.docx",
		".~lock.MATERI PB 13 UKHUWAH.pptx#",
		".~lock.doc.odt#",
	} {
		if !isIgnoredName(n) {
			t.Fatalf("%s should be ignored", n)
		}
	}
	if isIgnoredName("report.docx") || strings.HasPrefix("report.docx", "~$") {
		t.Fatal("normal file ignored")
	}
}

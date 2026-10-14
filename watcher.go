package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	queueSize     = 8192
	retryBase     = 250 * time.Millisecond
	retryMax      = 5 * time.Second
	eventsBufSize = 1024
)

// job is one file waiting to be sorted. Busy files are re-queued with backoff
// until deadline, so a locked file never occupies a worker.
type job struct {
	path     string
	deadline time.Time
	attempt  int
}

// Watcher reacts to OS file-system events (no polling while idle) and routes files
// using a fixed-size worker pool.
type Watcher struct {
	cfg    *Config
	log    *log.Logger
	fsw    *fsnotify.Watcher
	moveMu sync.Mutex // serialises resolve+move so two files can't pick the same name

	mu       sync.Mutex
	inflight map[string]struct{}
	jobs     chan job
	wg       sync.WaitGroup

	// handle processes one job and reports whether it should be retried later.
	// It is a field so tests can inject failures (e.g. panics).
	handle func(ctx context.Context, j job) (retry bool)
}

// NewWatcher creates a watcher bound to cfg.WatchDirectory.
func NewWatcher(cfg *Config, logger *log.Logger) (*Watcher, error) {
	fsw, err := fsnotify.NewBufferedWatcher(eventsBufSize)
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	if err := fsw.Add(cfg.WatchDirectory); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("watch %s: %w", cfg.WatchDirectory, err)
	}
	w := &Watcher{
		cfg:      cfg,
		log:      logger,
		fsw:      fsw,
		inflight: make(map[string]struct{}),
		jobs:     make(chan job, queueSize),
	}
	w.handle = w.process
	return w, nil
}

// Run blocks, dispatching events until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	defer w.fsw.Close()
	w.log.Printf("watching %s (%d workers)", w.cfg.WatchDirectory, w.cfg.MaxWorkers)

	for i := 0; i < w.cfg.MaxWorkers; i++ {
		w.wg.Add(1)
		go w.worker(ctx)
	}
	if w.cfg.SweepOnStart != nil && *w.cfg.SweepOnStart {
		w.sweep(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			w.wg.Wait()
			return nil
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			// Create covers new files and the final rename of "x.crdownload" -> "x.ext";
			// Write re-triggers files whose earlier wait timed out. Duplicates are dropped.
			if ev.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				w.dispatch(ctx, ev.Name)
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			w.log.Printf("watcher error: %v", err)
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// Too many events at once (e.g. a huge archive extracted): some were
				// lost, so rescan the folder to catch the files we never heard about.
				w.log.Printf("event overflow: rescanning %s", w.cfg.WatchDirectory)
				w.sweep(ctx)
			}
		}
	}
}

// sweep handles files already present in the watched folder.
func (w *Watcher) sweep(ctx context.Context) {
	entries, err := os.ReadDir(w.cfg.WatchDirectory)
	if err != nil {
		w.log.Printf("sweep: %v", err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			w.dispatch(ctx, filepath.Join(w.cfg.WatchDirectory, e.Name()))
		}
	}
}

// dispatch cheaply filters a path and, if it is a candidate, queues it.
func (w *Watcher) dispatch(ctx context.Context, path string) {
	name := filepath.Base(path)
	if isIgnoredName(name) || w.cfg.IsExcluded(filepath.Ext(name)) {
		return
	}
	if !w.claim(path) {
		return // already queued or being processed
	}
	w.enqueue(ctx, job{path: path, deadline: time.Now().Add(w.cfg.LockTimeout())})
}

func (w *Watcher) enqueue(ctx context.Context, j job) {
	if ctx.Err() != nil {
		w.release(j.path)
		return
	}
	select {
	case w.jobs <- j:
	default:
		w.log.Printf("queue full, dropping %s (it will be retried on its next change or rescan)", filepath.Base(j.path))
		w.release(j.path)
	}
}

func (w *Watcher) claim(path string) bool {
	key := strings.ToLower(path)
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, busy := w.inflight[key]; busy {
		return false
	}
	w.inflight[key] = struct{}{}
	return true
}

func (w *Watcher) release(path string) {
	w.mu.Lock()
	delete(w.inflight, strings.ToLower(path))
	w.mu.Unlock()
}

func (w *Watcher) worker(ctx context.Context) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-w.jobs:
			w.run(ctx, j)
		}
	}
}

// run executes one job, then either finishes it or schedules a retry via a timer
// (a timer holds no goroutine, so thousands of busy files cost almost nothing).
func (w *Watcher) run(ctx context.Context, j job) {
	if !w.safeHandle(ctx, j) {
		w.release(j.path)
		return
	}
	j.attempt++
	if time.Now().After(j.deadline) {
		w.log.Printf("gave up on %s: still busy after %s", filepath.Base(j.path), w.cfg.LockTimeout())
		w.release(j.path)
		return
	}
	time.AfterFunc(backoff(j.attempt), func() { w.enqueue(ctx, j) })
}

// safeHandle shields the whole process from a panic caused by a single file.
func (w *Watcher) safeHandle(ctx context.Context, j job) (retry bool) {
	defer func() {
		if r := recover(); r != nil {
			w.log.Printf("PANIC while processing %s: %v\n%s", filepath.Base(j.path), r, debug.Stack())
			retry = false
		}
	}()
	return w.handle(ctx, j)
}

// backoff grows 250ms, 500ms, 1s, 2s, 4s and then stays at 5s.
func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 5 {
		shift = 5
	}
	d := retryBase << uint(shift)
	if d > retryMax {
		d = retryMax
	}
	return d
}

// process makes one attempt to sort a file. It returns true only when the file is
// still busy and should be retried later.
func (w *Watcher) process(ctx context.Context, j job) (retry bool) {
	path := j.path
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false // vanished (already moved/renamed) or a folder
	}
	category, ok := w.cfg.CategoryFor(filepath.Ext(path))
	if !ok {
		return false
	}

	ready, err := CheckFileReady(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			w.log.Printf("skip %s: %v", filepath.Base(path), err)
		}
		return false
	}
	if !ready {
		return true
	}

	w.moveMu.Lock()
	dest, err := MoveToDir(path, filepath.Join(w.cfg.WatchDirectory, category))
	w.moveMu.Unlock()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			w.log.Printf("move %s failed (file left in place): %v", filepath.Base(path), err)
		}
		return false
	}
	w.log.Printf("moved %s -> %s", filepath.Base(path), dest)
	return false
}

// isIgnoredName skips housekeeping files that must never be sorted: Windows metadata
// and Office's hidden "~$name.docx" owner-lock files (moving those corrupts open docs).
func isIgnoredName(name string) bool {
	if strings.HasPrefix(name, "~$") {
		return true
	}
	switch strings.ToLower(name) {
	case "desktop.ini", "thumbs.db":
		return true
	}
	return false
}

package main

import (
	"log"
	"os"
	"sync"
)

const maxLogBytes = 1 << 20 // 1 MiB; current log is rotated to <name>.old

// rotatingFile is an io.Writer that appends to a file and rotates it while the app
// is running, so a months-long session cannot grow the log without bound.
// Logging failures never propagate: a background app must not die over its log.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func newRotatingFile(path string, max int64) *rotatingFile {
	r := &rotatingFile{path: path, max: max}
	r.open()
	return r
}

func (r *rotatingFile) open() {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		r.f = nil
		return
	}
	r.f = f
	if info, err := f.Stat(); err == nil {
		r.size = info.Size()
	}
}

func (r *rotatingFile) rotate() {
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
	_ = os.Remove(r.path + ".old")
	_ = os.Rename(r.path, r.path+".old")
	r.size = 0
	r.open()
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		r.open()
	}
	if r.f != nil && r.size+int64(len(p)) > r.max {
		r.rotate()
	}
	if r.f != nil {
		if n, err := r.f.Write(p); err == nil {
			r.size += int64(n)
		}
	}
	return len(p), nil
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil {
		err := r.f.Close()
		r.f = nil
		return err
	}
	return nil
}

// setupLogger returns a timestamped logger writing to a self-rotating file.
func setupLogger(path string) (*log.Logger, func()) {
	rf := newRotatingFile(path, maxLogBytes)
	return log.New(rf, "", log.LstdFlags), func() { rf.Close() }
}

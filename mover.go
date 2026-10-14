package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// ResolveUniquePath returns a non-existing path inside destDir for fileName.
// "a.pdf" becomes "a (1).pdf", "a (2).pdf", ...; as a last resort a timestamp is used.
func ResolveUniquePath(destDir, fileName string) string {
	candidate := filepath.Join(destDir, fileName)
	if !pathExists(candidate) {
		return candidate
	}
	ext := filepath.Ext(fileName)
	stem := strings.TrimSuffix(fileName, ext)
	for i := 1; i <= 9999; i++ {
		c := filepath.Join(destDir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if !pathExists(c) {
			return c
		}
	}
	now := time.Now()
	return filepath.Join(destDir, fmt.Sprintf("%s_%s_%03d%s",
		stem, now.Format("20060102_150405"), now.Nanosecond()/1e6, ext))
}

// MoveToDir moves src into destDir (created if missing) without ever overwriting an
// existing file, and returns the final destination path. If another process grabs the
// chosen name between the check and the move, a fresh unique name is picked.
func MoveToDir(src, destDir string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", destDir, err)
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		dest := ResolveUniquePath(destDir, filepath.Base(src))
		err := MoveFile(src, dest)
		if err == nil {
			return dest, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		lastErr = err
	}
	return "", lastErr
}

// MoveFile moves src to dest. On the same volume this is one atomic, no-overwrite
// rename. If that fails for another reason (e.g. different drive) it falls back to a
// verified copy + delete. It never overwrites an existing dest (returns os.ErrExist).
func MoveFile(src, dest string) error {
	err := renameNoReplace(src, dest)
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) && !pathExists(src) {
		return err
	}
	if cerr := copyThenRemove(src, dest); cerr != nil {
		if errors.Is(cerr, os.ErrExist) {
			return cerr
		}
		return fmt.Errorf("rename failed (%v); copy fallback failed: %w", err, cerr)
	}
	return nil
}

// verifyCopied checks the copy is byte-for-byte the same size as the original and
// that the source was not modified while copying. Only then may the source be deleted.
func verifyCopied(src, dest string, srcInfo os.FileInfo) error {
	d, err := os.Stat(dest)
	if err != nil {
		return fmt.Errorf("verify destination: %w", err)
	}
	if d.Size() != srcInfo.Size() {
		return fmt.Errorf("verify: destination is %d bytes, expected %d", d.Size(), srcInfo.Size())
	}
	s, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("verify source: %w", err)
	}
	if s.Size() != srcInfo.Size() || !s.ModTime().Equal(srcInfo.ModTime()) {
		return errors.New("verify: source changed while copying")
	}
	return nil
}

// copyThenRemove copies src to dest and deletes src only after verification.
// On ANY failure the (partial) dest is removed and src is left untouched, so a
// failed move can never lose data.
func copyThenRemove(src, dest string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dest) // never leave a partial copy behind
		}
	}()

	var n int64
	if n, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if n != info.Size() {
		out.Close()
		err = fmt.Errorf("copied %d of %d bytes", n, info.Size())
		return err
	}
	if err = out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = verifyCopied(src, dest, info); err != nil {
		return err
	}
	_ = os.Chtimes(dest, time.Now(), info.ModTime())
	in.Close() // Windows cannot delete a file that is still open
	err = os.Remove(src)
	return err
}

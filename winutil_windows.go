//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// acquireSingleInstance creates a named kernel mutex derived from key. If another
// process already holds it, already is true. The OS destroys the mutex when the
// owning process dies (even on crash/kill), so it can never go stale.
func acquireSingleInstance(key string) (release func(), already bool, err error) {
	name, err := windows.UTF16PtrFromString(`Local\WinAutoSort-` + key)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			if h != 0 {
				windows.CloseHandle(h)
			}
			return func() {}, true, nil
		}
		return nil, false, err
	}
	return func() { windows.CloseHandle(h) }, false, nil
}

// instanceKey identifies a watched folder: two copies watching the same folder
// collide, copies watching different folders may coexist.
func instanceKey(watchDir string) string {
	abs, err := filepath.Abs(watchDir)
	if err != nil {
		abs = watchDir
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return hex.EncodeToString(sum[:8])
}

// showError pops up an error message box.
func showError(text string) {
	t, err1 := windows.UTF16PtrFromString(text)
	c, err2 := windows.UTF16PtrFromString("WinAutoSort")
	if err1 != nil || err2 != nil {
		return
	}
	const mbOK, mbIconError = 0x0, 0x10
	windows.MessageBox(0, t, c, mbOK|mbIconError)
}

// showInfo pops up an information message box.
func showInfo(text string) {
	t, err1 := windows.UTF16PtrFromString(text)
	c, err2 := windows.UTF16PtrFromString("WinAutoSort")
	if err1 != nil || err2 != nil {
		return
	}
	const mbOK, mbIconInformation = 0x0, 0x40
	windows.MessageBox(0, t, c, mbOK|mbIconInformation)
}

// askYesNo displays a Windows Yes/No prompt. Returns true if user clicked Yes.
func askYesNo(title, text string) bool {
	t, err1 := windows.UTF16PtrFromString(text)
	c, err2 := windows.UTF16PtrFromString(title)
	if err1 != nil || err2 != nil {
		return false
	}
	const (
		mbYesNo          = 0x4
		mbIconQuestion   = 0x20
		mbDefButton2     = 0x100 // Default to 'No' for safety
		idYes            = 6
	)
	ret, _ := windows.MessageBox(0, t, c, mbYesNo|mbIconQuestion|mbDefButton2)
	return ret == idYes
}

// fixLongPath adds the \\?\ prefix for paths beyond the classic 260-char MAX_PATH
// limit. Needed for raw Win32 calls; Go's os package already does this itself.
func fixLongPath(p string) string {
	if len(p) < 248 || strings.HasPrefix(p, `\\?\`) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + abs[2:]
	}
	return `\\?\` + abs
}

// renameNoReplace moves src to dest atomically and fails with os.ErrExist instead of
// overwriting (os.Rename on Windows silently replaces an existing file).
// Cross-volume moves fail here and are handled by the copy fallback.
func renameNoReplace(src, dest string) error {
	from, err := windows.UTF16PtrFromString(fixLongPath(src))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(fixLongPath(dest))
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, 0); err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) && (errno == windows.ERROR_ALREADY_EXISTS || errno == windows.ERROR_FILE_EXISTS) {
			return fmt.Errorf("destination exists: %w", os.ErrExist)
		}
		return err
	}
	return nil
}

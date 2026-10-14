package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped config.json must be internally consistent: valid categories, lowercase
// dotted extensions, no extension mapped twice and none both routed and excluded.
func TestShippedConfigIsConsistent(t *testing.T) {
	raw, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}

	seen := map[string]string{}
	cats := map[string]bool{}
	for _, r := range cfg.Rules {
		if !validCategory(r.Category) {
			t.Errorf("bad category %q", r.Category)
		}
		if cats[r.Category] {
			t.Errorf("category %q defined twice", r.Category)
		}
		cats[r.Category] = true
		if len(r.Extensions) == 0 {
			t.Errorf("category %q has no extensions", r.Category)
		}
		for _, e := range r.Extensions {
			if e != strings.ToLower(e) || !strings.HasPrefix(e, ".") || len(e) < 2 {
				t.Errorf("%q in %s is not a lowercase dotted extension", e, r.Category)
			}
			if prev, dup := seen[e]; dup {
				t.Errorf("%q is in both %s and %s", e, prev, r.Category)
			}
			seen[e] = r.Category
		}
	}
	for _, e := range cfg.ExcludeExtensions {
		if cat, clash := seen[e]; clash {
			t.Errorf("%q is both excluded and routed to %s", e, cat)
		}
	}
	if cfg.DefaultFallback == "" || cats[cfg.DefaultFallback] {
		t.Errorf("default_fallback %q must be set and distinct from rule categories", cfg.DefaultFallback)
	}
	for _, want := range []string{"image_download", "video_download", "audio_download", "document_download",
		"archive_download", "installer_download", "font_download", "code_download"} {
		if !cats[want] {
			t.Errorf("missing category %s", want)
		}
	}
}

func TestShippedConfigRoutesCommonFiles(t *testing.T) {
	cfg, err := LoadConfig("config.json")
	if err != nil {
		t.Skipf("default watch dir unavailable here: %v", err)
	}
	cases := map[string]string{
		".JPG": "image_download", ".png": "image_download", ".heic": "image_download",
		".mp4": "video_download", ".mkv": "video_download", ".srt": "video_download",
		".mp3": "audio_download", ".flac": "audio_download", ".m4a": "audio_download",
		".pdf": "document_download", ".docx": "document_download", ".xlsx": "document_download",
		".zip": "archive_download", ".7z": "archive_download", ".exe": "installer_download",
		".msi": "installer_download", ".iso": "disk_image_download", ".ttf": "font_download",
		".psd": "design_download", ".py": "code_download", ".ts": "code_download",
		".sqlite": "data_download", ".epub": "ebook_download",
		".torrent": "file_download", ".lnk": "file_download", "": "file_download",
	}
	for ext, want := range cases {
		if got, ok := cfg.CategoryFor(ext); !ok || got != want {
			t.Errorf("%q -> %q (ok=%v), want %q", ext, got, ok, want)
		}
	}
	for _, e := range []string{".crdownload", ".part", ".tmp", ".download", ".opdownload", ".!qb"} {
		if !cfg.IsExcluded(e) {
			t.Errorf("%s should be excluded", e)
		}
	}
}

func TestRotatingLogBounded(t *testing.T) {
	p := filepath.Join(t.TempDir(), "autosort.log")
	rf := newRotatingFile(p, 200)
	line := strings.Repeat("x", 49) + "\n"
	for i := 0; i < 50; i++ {
		if n, err := rf.Write([]byte(line)); err != nil || n != len(line) {
			t.Fatalf("write must never fail: %d %v", n, err)
		}
	}
	rf.Close()
	cur, err := os.Stat(p)
	if err != nil || cur.Size() > 200 {
		t.Fatalf("current log not bounded: %v %v", cur, err)
	}
	old, err := os.Stat(p + ".old")
	if err != nil || old.Size() > 200 {
		t.Fatalf("rotated log missing/unbounded: %v %v", old, err)
	}
}

func TestRotatingLogSurvivesBadPath(t *testing.T) {
	rf := newRotatingFile(filepath.Join(t.TempDir(), "no-such-dir", "x.log"), 100)
	if n, err := rf.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("unwritable log must be silently discarded: %d %v", n, err)
	}
}

func TestVerifyCopied(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "s"), filepath.Join(dir, "d")
	write(t, src, "12345")
	write(t, dst, "12345")
	info, _ := os.Stat(src)
	if err := verifyCopied(src, dst, info); err != nil {
		t.Fatalf("matching copy rejected: %v", err)
	}
	write(t, dst, "123") // truncated copy
	if err := verifyCopied(src, dst, info); err == nil {
		t.Fatal("truncated copy must be rejected")
	}
	write(t, dst, "12345")
	write(t, src, "123456789") // source grew while copying
	if err := verifyCopied(src, dst, info); err == nil {
		t.Fatal("changed source must be rejected")
	}
}

func TestCopyFallbackNeverOverwritesOrLosesData(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "s"), filepath.Join(dir, "d")
	write(t, src, "new")
	write(t, dst, "precious")
	if err := copyThenRemove(src, dst); err == nil {
		t.Fatal("must refuse existing destination")
	}
	if b, _ := os.ReadFile(dst); string(b) != "precious" {
		t.Fatal("existing destination was destroyed")
	}
	if b, _ := os.ReadFile(src); string(b) != "new" {
		t.Fatal("source was lost")
	}
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandWindowsEnv(t *testing.T) {
	t.Setenv("WAS_TEST_VAR", `C:\Temp`)
	if got := ExpandWindowsEnv(`%WAS_TEST_VAR%\Downloads`); got != `C:\Temp\Downloads` {
		t.Fatalf("got %q", got)
	}
	if got := ExpandWindowsEnv(`%WAS_UNKNOWN_VAR%\x`); got != `%WAS_UNKNOWN_VAR%\x` {
		t.Fatalf("unknown var should be preserved, got %q", got)
	}
}

func TestPrepareAndLookup(t *testing.T) {
	cfg := &Config{
		WatchDirectory:    t.TempDir(),
		Rules:             []Rule{{Category: "image_download", Extensions: []string{".JPG", "png"}}},
		DefaultFallback:   "file_download",
		ExcludeExtensions: []string{".CrDownload", "part"},
	}
	if err := cfg.prepare(); err != nil {
		t.Fatal(err)
	}
	if c, ok := cfg.CategoryFor(".jpg"); !ok || c != "image_download" {
		t.Fatalf("jpg -> %q %v", c, ok)
	}
	if c, ok := cfg.CategoryFor(".PNG"); !ok || c != "image_download" {
		t.Fatalf("PNG -> %q %v", c, ok)
	}
	if c, ok := cfg.CategoryFor(".xyz"); !ok || c != "file_download" {
		t.Fatalf("fallback -> %q %v", c, ok)
	}
	if !cfg.IsExcluded(".crdownload") || !cfg.IsExcluded(".PART") || cfg.IsExcluded(".png") {
		t.Fatal("exclusion mismatch")
	}
	if cfg.LockTimeoutSeconds != 7200 || cfg.MaxWorkers != 4 {
		t.Fatalf("defaults: timeout=%d workers=%d", cfg.LockTimeoutSeconds, cfg.MaxWorkers)
	}
}

func TestNoFallbackLeavesFileAlone(t *testing.T) {
	cfg := &Config{WatchDirectory: t.TempDir()}
	if err := cfg.prepare(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.CategoryFor(".xyz"); ok {
		t.Fatal("expected no category")
	}
}

func TestRejectsBadCategory(t *testing.T) {
	for _, bad := range []string{"..", `a\b`, "a/b", "", "C:"} {
		cfg := &Config{WatchDirectory: t.TempDir(), Rules: []Rule{{Category: bad, Extensions: []string{".a"}}}}
		if err := cfg.prepare(); err == nil {
			t.Fatalf("category %q should be rejected", bad)
		}
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	body := `{"watch_directory": ` + jsonQuote(dir) + `, "rules": [{"category":"v","extensions":[".mp4"]}]}`
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := cfg.CategoryFor(".mp4"); c != "v" {
		t.Fatalf("got %q", c)
	}
	if _, err := LoadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func jsonQuote(s string) string {
	out := `"`
	for _, r := range s {
		if r == '\\' {
			out += `\\`
		} else {
			out += string(r)
		}
	}
	return out + `"`
}

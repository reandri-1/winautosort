package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Rule maps a set of file extensions to a category (sub-folder) name.
type Rule struct {
	Category   string   `json:"category"`
	Extensions []string `json:"extensions"`
}

// Config mirrors config.json and is the single source of truth for sorting logic.
type Config struct {
	// WatchDirectory is the folder to monitor. Windows-style %VAR% references are expanded.
	WatchDirectory string `json:"watch_directory"`
	// Rules maps extensions to category sub-folders created inside WatchDirectory.
	Rules []Rule `json:"rules"`
	// DefaultFallback is the category used for extensions without a matching rule.
	// When empty, unmatched files are left untouched.
	DefaultFallback string `json:"default_fallback"`
	// ExcludeExtensions lists in-progress/temporary extensions that must never be moved.
	ExcludeExtensions []string `json:"exclude_extensions"`
	// SweepOnStart sorts files already present in WatchDirectory when the app starts.
	// If nil in config.json, setup prompt will ask user on first launch.
	SweepOnStart *bool `json:"sweep_on_start"`
	// LockTimeoutSeconds is how long to keep retrying a busy file before giving up.
	LockTimeoutSeconds int `json:"lock_timeout_seconds"`
	// MaxWorkers bounds how many files are processed concurrently (default 4).
	MaxWorkers int `json:"max_workers"`

	extToCategory map[string]string
	excluded      map[string]struct{}
}

var winEnvPattern = regexp.MustCompile(`%([^%]+)%`)

// ExpandWindowsEnv expands Windows-style %VAR% references (e.g. %USERPROFILE%).
// Unknown variables are left untouched so the error message shows the original text.
func ExpandWindowsEnv(s string) string {
	return winEnvPattern.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := os.LookupEnv(m[1 : len(m)-1]); ok {
			return v
		}
		return m
	})
}

// normalizeExt lowercases an extension and guarantees a leading dot. Empty input yields "".
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return ""
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

// validCategory rejects names that could escape WatchDirectory (path traversal).
func validCategory(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `\/:*?"<>|`) && strings.TrimSpace(name) == name
}

// LoadConfig reads, expands, validates and indexes the JSON configuration at path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.prepare(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveConfig writes the configuration back to disk with nice indentation.
func SaveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// prepare expands paths, validates values and builds fast lookup tables.
func (c *Config) prepare() error {
	c.WatchDirectory = filepath.Clean(ExpandWindowsEnv(strings.TrimSpace(c.WatchDirectory)))
	if strings.TrimSpace(c.WatchDirectory) == "" || c.WatchDirectory == "." {
		return fmt.Errorf("config: watch_directory is required")
	}
	info, err := os.Stat(c.WatchDirectory)
	if err != nil {
		return fmt.Errorf("config: watch_directory %q: %w", c.WatchDirectory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("config: watch_directory %q is not a directory", c.WatchDirectory)
	}

	if c.DefaultFallback != "" && !validCategory(c.DefaultFallback) {
		return fmt.Errorf("config: invalid default_fallback %q", c.DefaultFallback)
	}
	if c.LockTimeoutSeconds <= 0 {
		c.LockTimeoutSeconds = 7200
	}
	if c.MaxWorkers <= 0 {
		c.MaxWorkers = 4
	}
	if c.MaxWorkers > 64 {
		c.MaxWorkers = 64
	}

	c.extToCategory = make(map[string]string)
	for _, r := range c.Rules {
		if !validCategory(r.Category) {
			return fmt.Errorf("config: invalid category name %q", r.Category)
		}
		for _, e := range r.Extensions {
			if ext := normalizeExt(e); ext != "" {
				if _, dup := c.extToCategory[ext]; !dup { // first rule wins
					c.extToCategory[ext] = r.Category
				}
			}
		}
	}

	c.excluded = make(map[string]struct{})
	for _, e := range c.ExcludeExtensions {
		if ext := normalizeExt(e); ext != "" {
			c.excluded[ext] = struct{}{}
		}
	}
	return nil
}

// LockTimeout returns the unlock wait limit as a duration.
func (c *Config) LockTimeout() time.Duration {
	return time.Duration(c.LockTimeoutSeconds) * time.Second
}

// IsExcluded reports whether ext (e.g. ".crdownload") must be ignored.
func (c *Config) IsExcluded(ext string) bool {
	_, ok := c.excluded[normalizeExt(ext)]
	return ok
}

// CategoryFor returns the destination category for ext. The bool is false when the
// file should stay where it is (no rule and no default_fallback).
func (c *Config) CategoryFor(ext string) (string, bool) {
	if cat, ok := c.extToCategory[normalizeExt(ext)]; ok {
		return cat, true
	}
	if c.DefaultFallback != "" {
		return c.DefaultFallback, true
	}
	return "", false
}

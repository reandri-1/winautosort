package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
)

func main() {
	configFlag := flag.String("config", "", "path to config.json (default: next to the executable, then the working directory)")
	setupFlag := flag.Bool("setup", false, "force setup dialog to configure sweep on start")
	flag.Parse()

	configPath := resolveConfigPath(*configFlag)
	logger, closeLog := setupLogger(filepath.Join(filepath.Dir(configPath), "autosort.log"))

	// Last line of defence: log any unexpected panic and tell the user, instead of
	// dying silently (the windowsgui build has no console to show a stack trace).
	defer func() {
		if r := recover(); r != nil {
			logger.Printf("FATAL PANIC: %v\n%s", r, debug.Stack())
			closeLog()
			showError(fmt.Sprintf("WinAutoSort crashed: %v\nSee autosort.log for details.", r))
			os.Exit(2)
		}
	}()

	fatal := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		logger.Printf("fatal: %s", msg)
		closeLog()
		showError(msg)
		os.Exit(1)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		fatal("Cannot start:\n%v\n\nConfig file: %s", err, configPath)
	}

	// First-launch or explicit --setup: prompt user for SweepOnStart preference.
	if cfg.SweepOnStart == nil || *setupFlag {
		promptText := fmt.Sprintf(
			"Selamat datang di WinAutoSort!\n\n"+
				"Target folder: %s\n\n"+
				"Apakah Anda ingin WinAutoSort otomatis menyapu & merapikan file lama yang SUDAH ADA di folder ini setiap kali aplikasi berjalan?\n\n"+
				"• Klik YES: Semua file lama langsung disortir ke folder kategorinya.\n"+
				"• Klik NO: Hanya sortir file baru yang diunduh setelah ini.\n\n"+
				"(Pilihan ini akan disimpan di config.json dan bisa diubah kapan saja)",
			cfg.WatchDirectory,
		)
		userChoice := askYesNo("WinAutoSort - Pengaturan Awal", promptText)
		cfg.SweepOnStart = &userChoice
		if err := SaveConfig(configPath, cfg); err != nil {
			logger.Printf("warning: gagal menyimpan pilihan sweep ke %s: %v", configPath, err)
		} else {
			logger.Printf("pengaturan awal tersimpan: sweep_on_start = %v", userChoice)
			statusMsg := "Hanya file baru yang akan disortir."
			if userChoice {
				statusMsg = "File lama dan file baru akan otomatis disortir."
			}
			showInfo(fmt.Sprintf("Pengaturan tersimpan!\n\nStatus: %s\n\nWinAutoSort sekarang aktif di latar belakang.", statusMsg))
		}
	}

	// Refuse to run twice on the same folder (two watchers would race each other).
	release, already, err := acquireSingleInstance(instanceKey(cfg.WatchDirectory))
	if err != nil {
		fatal("Cannot create single-instance lock: %v", err)
	}
	if already {
		logger.Printf("another instance is already watching %s; exiting", cfg.WatchDirectory)
		closeLog()
		os.Exit(0)
	}
	defer release()
	defer closeLog()

	w, err := NewWatcher(cfg, logger)
	if err != nil {
		fatal("Cannot start watcher:\n%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Printf("WinAutoSort started (config: %s)", configPath)
	if err := w.Run(ctx); err != nil {
		logger.Printf("fatal: %v", err)
		os.Exit(1)
	}
	logger.Printf("WinAutoSort stopped")
}

// resolveConfigPath picks an explicit path, else config.json beside the executable
// (the right place for a background process), else the working directory.
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return explicit
		}
		return abs
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "config.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	abs, err := filepath.Abs("config.json")
	if err != nil {
		return "config.json"
	}
	return abs
}

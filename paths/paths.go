package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".clawlet"), nil
}

func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func SessionsDir() string {
	dir, err := ConfigDir()
	if err != nil {
		// Should never happen after startup; keep a sane fallback.
		return ".clawlet/sessions"
	}
	return filepath.Join(dir, "sessions")
}

func CronStorePath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ".clawlet/cron.json"
	}
	return filepath.Join(dir, "cron.json")
}

func WorkspaceDir() string {
	dir, err := ConfigDir()
	if err != nil {
		return ".clawlet/workspace"
	}
	return filepath.Join(dir, "workspace")
}

func EnsureStateDirs() error {
	cfgDir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", cfgDir, err)
	}
	sdir := SessionsDir()
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", sdir, err)
	}
	return nil
}

// Package appdir locates the per-user directory that holds the weblinks
// store, its backups, the classification prompt and the log, and seeds
// default files into it. Everything lives in one directory so that a single
// path covers the whole installation: $XDG_CONFIG_HOME/weblinks, falling back
// to ~/.config/weblinks, everywhere except Windows, which uses %AppData%.
package appdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// name is the directory created under the base configuration directory.
const name = "weblinks"

// dirPerm keeps the directory private, since it holds the user's weblinks.
const dirPerm = 0o700

// filePerm is the mode of a seeded file. The private directory already
// restricts access, so this matches what an editor would create.
const filePerm = 0o644

// Dir returns the weblinks directory without creating it. On Windows it is
// weblinks under os.UserConfigDir (%AppData%). Elsewhere it is weblinks under
// $XDG_CONFIG_HOME when that is an absolute path, which the XDG Base
// Directory specification requires, and under ~/.config otherwise.
func Dir() (string, error) {
	if runtime.GOOS == "windows" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate the configuration directory: %w", err)
		}
		return filepath.Join(base, name), nil
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(base) {
		return filepath.Join(base, name), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate the home directory: %w", err)
	}
	return filepath.Join(home, ".config", name), nil
}

// Create makes dir and any missing parents, with private permissions for
// each directory it creates. An existing directory is left as it is.
func Create(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}

// Seed writes content to path when no file exists there yet and reports
// whether it did. An existing file is never touched, so a user's edits
// survive upgrades. The parent directory must already exist.
func Seed(path string, content []byte) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("seed %s: %w", path, err)
	}
	_, writeErr := f.Write(content)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		// A partial file would never be replaced, so remove it and let the
		// next start try again.
		os.Remove(path)
		return false, fmt.Errorf("seed %s: %w", path, err)
	}
	return true, nil
}

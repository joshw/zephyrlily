package creds

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Dir returns the directory holding zlily's per-user files, creating nothing.
//
// $ZLILY_CONFIG_DIR wins when set (tests use it, and it lets one machine keep
// separate identities). Otherwise the XDG location, which is where a terminal
// program's dotfiles belong on the Linux hosts zlily mostly runs on; Windows
// falls back to the OS answer, since it has no ~/.config.
func Dir() (string, error) {
	if d := os.Getenv("ZLILY_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "zlily"), nil
	}
	if runtime.GOOS == "windows" {
		d, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate config dir: %w", err)
		}
		return filepath.Join(d, "zlily"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "zlily"), nil
}

// ensureDir returns the config dir, creating it 0700 if it does not exist. The
// credentials file inside it is 0600, but a private directory means a password
// is never briefly world-readable while it is being written.
func ensureDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}

func credentialsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials"), nil
}

func configPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// writePrivate writes data to path through a temporary file in the same
// directory, so a reader never sees a half-written file and an interrupted
// write cannot lose what was already there.
func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	// os.CreateTemp already opens 0600, which is why the password is never on
	// disk under looser permissions even for an instant.
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename onto %s: %w", path, err)
	}
	return nil
}

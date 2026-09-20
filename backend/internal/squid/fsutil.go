package squid

import (
	"os"
	"path/filepath"
)

// writeFileAtomic replaces path with data by writing a temp file in the same
// directory and renaming it over the target, so a crash mid-write can never
// leave squid.conf (or a list file) half-written. The existing file's mode is
// preserved. If the directory isn't writable (only the file itself is), it
// falls back to an in-place write.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".squidadmin-*")
	if err != nil {
		return os.WriteFile(path, data, perm)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

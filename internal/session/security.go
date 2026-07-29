package session

import (
	"fmt"
	"os"
	"runtime"
)

func validatePrivateStoreDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("session store directory %q must not be a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("session store path %q is not a directory", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("session store directory %q is writable by group or other users", path)
	}
	return nil
}

func rejectSymlink(path, label string) error {
	info, err := os.Lstat(path)
	if errorsIsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	return nil
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

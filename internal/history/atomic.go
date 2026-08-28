package history

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func atomicWriteFile(path string, content []byte, mode os.FileMode) (retErr error) {
	return atomicWriteReader(path, bytes.NewReader(content), mode)
}

func atomicWriteReader(path string, content io.Reader, mode os.FileMode) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		if retErr != nil {
			_ = os.Remove(tempName)
		}
	}()

	if err := temp.Chmod(mode.Perm()); err != nil {
		return err
	}
	if _, err := io.Copy(temp, content); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tempName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func existingMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode()
	}
	return 0o644
}

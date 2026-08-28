//go:build !windows

package history

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}

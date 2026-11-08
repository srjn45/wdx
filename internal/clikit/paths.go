package clikit

import (
	"errors"
	"path/filepath"

	"github.com/spf13/cobra"
)

// Absolute makes path absolute against the working directory, which a
// server the program calls does not share; "" stays "".
func Absolute(cmd *cobra.Command, path string) (string, error) {
	switch {
	case path == "":
		return "", nil
	case filepath.IsAbs(path):
		return filepath.Clean(path), nil
	}
	dir := InvocationOf(cmd).Env.WorkDir
	if dir == "" {
		return "", errors.New("the working directory is unknown; give an absolute path")
	}
	return filepath.Join(dir, path), nil
}

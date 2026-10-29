// Package atomicfile writes files so that whoever reads them, or a crash,
// finds the old content or the new, never part of it: a temporary file
// beside the target is written and synced, renamed into place, and the
// directory synced so the rename itself survives a power loss.
package atomicfile

import (
	"io"
	"os"
	"path/filepath"
)

// Write writes data to path with mode.
func Write(path string, data []byte, mode os.FileMode) error {
	return write(path, mode, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// Copy copies the file src to dest with mode.
func Copy(dest, src string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return write(dest, mode, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}

func write(path string, mode os.FileMode, fill func(io.Writer) error) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := fill(tmp); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

package provisioner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalSource copies *.provisioners.yaml files from a local directory.
type LocalSource struct {
	Path string
}

func NewLocalSource(path string) *LocalSource {
	return &LocalSource{Path: path}
}

func (s *LocalSource) Sync(ctx context.Context, destDir string) error {
	matches, err := filepath.Glob(filepath.Join(s.Path, "*.provisioners.yaml"))
	if err != nil {
		return fmt.Errorf("glob provisioners: %w", err)
	}
	for _, src := range matches {
		dst := filepath.Join(destDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

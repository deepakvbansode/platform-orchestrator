package provisioner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/deepakvbansode/platform-orchestrator/internal/logger"
)

// LocalSource copies *.provisioners.yaml files from a local directory.
type LocalSource struct {
	Path string
}

func NewLocalSource(path string) *LocalSource {
	return &LocalSource{Path: path}
}

func (s *LocalSource) Sync(ctx context.Context, destDir string) error {
	log := logger.Get()
	log.Debug("local provisioner sync", "source_path", s.Path)
	matches, err := filepath.Glob(filepath.Join(s.Path, "*.provisioners.yaml"))
	if err != nil {
		return fmt.Errorf("glob provisioners: %w", err)
	}
	log.Debug("provisioner files found", "count", len(matches))
	for _, src := range matches {
		dst := filepath.Join(destDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
		}
		log.Debug("provisioner file copied", "file", filepath.Base(src))
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

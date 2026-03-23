package provisioner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/deepakvbansode/platform-orchestrator/internal/config"
	"github.com/deepakvbansode/platform-orchestrator/internal/logger"
)

// GitSource shallow-clones a git repo and copies *.provisioners.yaml from a subpath.
type GitSource struct {
	cfg config.GitProvConfig
}

func NewGitSource(cfg config.GitProvConfig) *GitSource {
	return &GitSource{cfg: cfg}
}

func (s *GitSource) Sync(ctx context.Context, destDir string) error {
	log := logger.Get()
	log.Debug("git provisioner sync", "url", s.cfg.URL, "ref", s.cfg.Ref, "path", s.cfg.Path)

	tmpDir, err := os.MkdirTemp("", "score-provisioners-*")
	if err != nil {
		return fmt.Errorf("create temp dir for git clone: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	url := s.cfg.URL
	env := os.Environ()

	switch s.cfg.Auth.Type {
	case "https":
		token := os.Getenv(s.cfg.Auth.TokenEnv)
		if token != "" {
			url = embedHTTPSToken(url, token)
		}
		log.Debug("git auth: https", "token_set", token != "")
	case "ssh":
		if s.cfg.Auth.SSHKeyFile != "" {
			env = append(env, fmt.Sprintf(
				"GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no",
				s.cfg.Auth.SSHKeyFile,
			))
		}
		log.Debug("git auth: ssh", "key_file", s.cfg.Auth.SSHKeyFile)
	}

	ref := s.cfg.Ref
	if ref == "" {
		ref = "main"
	}

	log.Debug("exec: git clone (shallow)", "ref", ref)
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", ref, url, tmpDir)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(out))
	}
	log.Debug("git clone completed")

	srcDir := tmpDir
	if s.cfg.Path != "" {
		srcDir = filepath.Join(tmpDir, s.cfg.Path)
	}

	matches, err := filepath.Glob(filepath.Join(srcDir, "*.provisioners.yaml"))
	if err != nil {
		return fmt.Errorf("glob provisioners: %w", err)
	}
	log.Debug("provisioner files found in repo", "count", len(matches))
	for _, src := range matches {
		dst := filepath.Join(destDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
		}
		log.Debug("provisioner file copied", "file", filepath.Base(src))
	}
	return nil
}

// embedHTTPSToken inserts a token into an HTTPS git URL.
// "https://github.com/org/repo.git" → "https://token@github.com/org/repo.git"
func embedHTTPSToken(rawURL, token string) string {
	const httpsPrefix = "https://"
	if len(rawURL) > len(httpsPrefix) && rawURL[:len(httpsPrefix)] == httpsPrefix {
		return httpsPrefix + token + "@" + rawURL[len(httpsPrefix):]
	}
	return rawURL
}

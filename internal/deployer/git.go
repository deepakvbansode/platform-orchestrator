package deployer

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/deepakvbansode/platform-orchestrator/internal/config"
	"github.com/deepakvbansode/platform-orchestrator/internal/interpolate"
	"github.com/deepakvbansode/platform-orchestrator/internal/logger"
)

// GitDeployer clones a git repo, copies the manifest, commits, and pushes.
type GitDeployer struct {
	name string
	cfg  config.GitDeployerConfig
}

func NewGitDeployer(name string, cfg config.GitDeployerConfig) *GitDeployer {
	return &GitDeployer{name: name, cfg: cfg}
}

func (d *GitDeployer) Name() string { return d.name }

func (d *GitDeployer) Deploy(ctx context.Context, req DeployRequest) error {
	log := logger.Get()
	vars := map[string]string{"org": req.Org, "env": req.Env, "workload": req.Workload}

	tmpDir, err := os.MkdirTemp("", "score-gitdeploy-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	url := interpolate.Expand(d.cfg.URL, vars)
	env := os.Environ()

	ref := d.cfg.Ref
	if ref == "" {
		ref = "main"
	}

	switch d.cfg.Auth.Type {
	case "https":
		token := os.Getenv(d.cfg.Auth.TokenEnv)
		if token != "" {
			url = embedHTTPSToken(url, token)
		}
		log.Debug("git deployer auth: https", "token_set", token != "")
	case "ssh":
		if d.cfg.Auth.SSHKeyFile != "" {
			env = append(env, fmt.Sprintf(
				"GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no",
				d.cfg.Auth.SSHKeyFile,
			))
		}
		log.Debug("git deployer auth: ssh", "key_file", d.cfg.Auth.SSHKeyFile)
	}

	run := func(dir string, args ...string) error {
		log.Debug("exec: git", "args", args)
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w\n%s", args, err, string(out))
		}
		return nil
	}

	log.Debug("cloning gitops repo", "url", d.cfg.URL, "ref", ref)
	if err := run("", "clone", "--depth", "1", "--branch", ref, url, tmpDir); err != nil {
		return err
	}

	destPath := filepath.Join(tmpDir, interpolate.Expand(d.cfg.Path, vars))
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("mkdir dest path: %w", err)
	}

	dst := filepath.Join(destPath, fmt.Sprintf("%s-%s-%s-manifests.yaml", req.Org, req.Env, req.Workload))
	if err := copyFile(req.ManifestsPath, dst); err != nil {
		return fmt.Errorf("copy manifests: %w", err)
	}
	log.Debug("manifest copied to gitops repo", "dest", dst)

	if err := run(tmpDir, "config", "user.email", "score-orchestrator@platform"); err != nil {
		return err
	}
	if err := run(tmpDir, "config", "user.name", "score-orchestrator"); err != nil {
		return err
	}
	relDst, err := filepath.Rel(tmpDir, dst)
	if err != nil {
		return fmt.Errorf("compute relative path: %w", err)
	}
	if err := run(tmpDir, "add", relDst); err != nil {
		return err
	}
	if err := run(tmpDir, "commit", "-m", fmt.Sprintf("deploy: %s/%s/%s", req.Org, req.Env, req.Workload)); err != nil {
		return err
	}
	log.Debug("pushing gitops commit", "ref", ref)
	return run(tmpDir, "push", "origin", ref)
}

// copyFile is a local utility. The same function exists in internal/provisioner/local.go
// but that is a separate package and cannot be imported from here.
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

// embedHTTPSToken inserts a token into an HTTPS git URL.
// "https://github.com/org/repo.git" → "https://token@github.com/org/repo.git"
func embedHTTPSToken(rawURL, token string) string {
	const httpsPrefix = "https://"
	if len(rawURL) > len(httpsPrefix) && rawURL[:len(httpsPrefix)] == httpsPrefix {
		return httpsPrefix + token + "@" + rawURL[len(httpsPrefix):]
	}
	return rawURL
}

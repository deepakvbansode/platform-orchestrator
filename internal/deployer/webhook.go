package deployer

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/score-spec/score-orchestrator/internal/config"
	"github.com/score-spec/score-orchestrator/internal/interpolate"
)

// WebhookDeployer fires an HTTP request to a configurable endpoint.
type WebhookDeployer struct {
	name string
	cfg  config.WebhookConfig
}

func NewWebhookDeployer(name string, cfg config.WebhookConfig) *WebhookDeployer {
	return &WebhookDeployer{name: name, cfg: cfg}
}

func (d *WebhookDeployer) Name() string { return d.name }

func (d *WebhookDeployer) Deploy(ctx context.Context, req DeployRequest) error {
	vars := map[string]string{"org": req.Org, "env": req.Env, "workload": req.Workload}

	url := interpolate.Expand(d.cfg.URL, vars)
	method := d.cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	body := interpolate.Expand(d.cfg.Body, vars)

	timeout := time.Duration(d.cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	httpClient := &http.Client{Timeout: timeout}

	httpReq, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	for k, v := range d.cfg.Headers {
		httpReq.Header.Set(k, interpolate.Expand(v, vars))
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

package pipeline

import "time"

// DeployRequest is the input to the deploy pipeline.
type DeployRequest struct {
	Org       string
	Env       string
	Workload  string
	ScoreYAML []byte // raw score.yaml content
}

// DeployResult is returned on pipeline success.
type DeployResult struct {
	Org          string    `json:"org"`
	Env          string    `json:"env"`
	Workload     string    `json:"workload"`
	Status       string    `json:"status"` // "success" | "deploy_failed"
	DeployersRun string    `json:"deployer_run"`
	DeployedAt   time.Time `json:"deployed_at"`
}

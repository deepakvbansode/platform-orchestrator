package action

import "time"

// ActionSpec represents the contents of a platform-action.yaml file.
type ActionSpec struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   ActionMetadata `yaml:"metadata"`
	Spec       ActionSpecBody `yaml:"spec"`
}

// ActionMetadata holds the identity fields of an action.
type ActionMetadata struct {
	Name        string            `yaml:"name"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// ActionSpecBody is the core spec section.
type ActionSpecBody struct {
	ResourceType string                 `yaml:"resourceType"`
	Operation    string                 `yaml:"operation"`
	Properties   map[string]interface{} `yaml:"properties"`
	Lifecycle    *ActionLifecycle       `yaml:"lifecycle,omitempty"`
	DependsOn    []string               `yaml:"dependsOn,omitempty"`
	Notify       *ActionNotify          `yaml:"notify,omitempty"`
}

// ActionLifecycle controls time-bound behaviour of a provisioned resource.
type ActionLifecycle struct {
	TTL         string `yaml:"ttl,omitempty"`
	AutoRevoke  bool   `yaml:"autoRevoke,omitempty"`
	Renewable   bool   `yaml:"renewable,omitempty"`
	MaxRenewals int    `yaml:"maxRenewals,omitempty"`
}

// ActionNotify specifies notification targets on success/failure.
type ActionNotify struct {
	OnSuccess []string `yaml:"onSuccess,omitempty"`
	OnFailure []string `yaml:"onFailure,omitempty"`
}

// ActionContext is injected by the orchestrator at runtime and is never part of
// the spec file. It carries verified identity information from the caller.
type ActionContext struct {
	User        string `json:"user"`
	Team        string `json:"team"`
	Role        string `json:"role"`
	Email       string `json:"email"`
	RequestedAt string `json:"requestedAt"`
	RequestID   string `json:"requestId"`
}

// ActionRequest is what the runner receives: a parsed spec plus injected context.
type ActionRequest struct {
	Spec    ActionSpec
	Context ActionContext
}

// Action status constants.
const (
	StatusSubmitted = "submitted"
	StatusDeployed  = "deployed"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// ActionResult is returned to the API caller after Run completes.
type ActionResult struct {
	ActionID string            `json:"actionId"`
	Status   string            `json:"status"`
	Outputs  map[string]string `json:"outputs,omitempty"`
	Message  string            `json:"message,omitempty"`
}

// ActionState is the persisted record of an action execution.
type ActionState struct {
	ActionID    string            `json:"actionId"`
	Status      string            `json:"status"`
	Outputs     map[string]string `json:"outputs,omitempty"`
	Message     string            `json:"message,omitempty"`
	RequestedAt time.Time         `json:"requestedAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

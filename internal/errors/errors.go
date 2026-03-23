package errors

import "fmt"

// Error codes as defined in the spec.
const (
	CodeInvalidInput          = "INVALID_INPUT"
	CodeInvalidScore          = "INVALID_SCORE"
	CodeStatePullFailed       = "STATE_PULL_FAILED"
	CodeProvisionerSyncFailed = "PROVISIONER_SYNC_FAILED"
	CodeScoreGenerateFailed   = "SCORE_GENERATE_FAILED"
	CodeStateConflict         = "STATE_CONFLICT"
	CodeDeployFailed          = "DEPLOY_FAILED"
	CodeStatePushFailed       = "STATE_PUSH_FAILED"
	CodeWorkloadNotFound      = "WORKLOAD_NOT_FOUND"
)

// OrchestratorError is the canonical error type for all pipeline failures.
type OrchestratorError struct {
	Code       string
	Message    string
	Detail     string
	Stage      string
	HTTPStatus int
}

func (e *OrchestratorError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Detail)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func New(code, message, detail, stage string, httpStatus int) *OrchestratorError {
	return &OrchestratorError{
		Code:       code,
		Message:    message,
		Detail:     detail,
		Stage:      stage,
		HTTPStatus: httpStatus,
	}
}

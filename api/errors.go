package api

import (
	"encoding/json"
	"net/http"

	oerr "github.com/deepakvbansode/platform-orchestrator/internal/errors"
)

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
	Stage   string `json:"stage,omitempty"`
}

func writeError(w http.ResponseWriter, oe *oerr.OrchestratorError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(oe.HTTPStatus)
	json.NewEncoder(w).Encode(errorResponse{
		Error: errorBody{
			Code:    oe.Code,
			Message: oe.Message,
			Detail:  oe.Detail,
			Stage:   oe.Stage,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

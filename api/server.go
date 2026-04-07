package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/deepakvbansode/platform-orchestrator/internal/action"
	"github.com/deepakvbansode/platform-orchestrator/internal/pipeline"
	"github.com/deepakvbansode/platform-orchestrator/internal/state"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	runner       *pipeline.Runner
	backend      state.StateBackend
	actionRunner *action.Runner
	actionState  action.ActionStateBackend
	port         int
}

func NewServer(runner *pipeline.Runner, backend state.StateBackend, actionRunner *action.Runner, actionState action.ActionStateBackend, port int) *Server {
	return &Server{
		runner:       runner,
		backend:      backend,
		actionRunner: actionRunner,
		actionState:  actionState,
		port:         port,
	}
}

func (s *Server) Start() error {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/api/v1/deploy", handleDeploy(s.runner))
	r.Get("/api/v1/workloads/{org}/{env}/{workload}/status", handleStatus(s.backend))
	r.Get("/api/v1/workloads/{org}/{env}/{workload}/manifest", handleManifest(s.backend))

	r.Post("/api/v1/actions", handleActionSubmit(s.actionRunner))
	r.Get("/api/v1/actions/{name}/status", handleActionStatus(s.actionState))

	r.Get("/healthz", handleHealthz())

	addr := fmt.Sprintf(":%d", s.port)
	return http.ListenAndServe(addr, r)
}

package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/score-spec/score-orchestrator/internal/pipeline"
	"github.com/score-spec/score-orchestrator/internal/state"
)

// Server holds the HTTP server and its dependencies.
type Server struct {
	runner  *pipeline.Runner
	backend state.StateBackend
	port    int
}

func NewServer(runner *pipeline.Runner, backend state.StateBackend, port int) *Server {
	return &Server{runner: runner, backend: backend, port: port}
}

func (s *Server) Start() error {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/api/v1/deploy", handleDeploy(s.runner))
	r.Get("/api/v1/workloads/{org}/{env}/{workload}/status", handleStatus(s.backend))
	r.Get("/api/v1/workloads/{org}/{env}/{workload}/manifest", handleManifest(s.backend))
	r.Get("/healthz", handleHealthz())

	addr := fmt.Sprintf(":%d", s.port)
	return http.ListenAndServe(addr, r)
}

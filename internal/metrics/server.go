package metrics

import (
	"context"
	"errors"
	"net/http"

	"planing_bot/internal/logging"
)

type Server struct {
	addr   string
	server *http.Server
	logger *logging.Logger
}

func NewServer(addr string, registry *Registry, logger *logging.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", registry.Handler())
	return &Server{
		addr: addr,
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
		logger: logger,
	}
}

func (s *Server) Run(ctx context.Context) {
	if s.logger != nil {
		s.logger.Info("metrics_server_started", logging.Fields{"addr": s.addr})
	}
	go func() {
		<-ctx.Done()
		_ = s.server.Shutdown(context.Background())
	}()
	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) && s.logger != nil {
		s.logger.Error("metrics_server_failed", err, logging.Fields{"addr": s.addr})
	}
}

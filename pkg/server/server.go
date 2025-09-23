package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/cors"
	sloghttp "github.com/samber/slog-http"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/healthcheck"
	"github.com/italypaleale/ddup/pkg/utils"
)

const (
	headerContentType = "Content-Type"
	jsonContentType   = "application/json; charset=utf-8"
)

// Server is the server based on Gin
type Server struct {
	hc healthcheck.StatusProvider

	appSrv  *http.Server
	handler http.Handler
	running atomic.Bool
	wg      sync.WaitGroup

	// Listener for the app server
	// This can be used for testing without having to start an actual TCP listener
	appListener net.Listener
}

// NewServerOpts contains options for the NewServer method
type NewServerOpts struct {
	HealthChecker healthcheck.StatusProvider
}

// NewServer creates a new Server object and initializes it
func NewServer(opts NewServerOpts) (*Server, error) {
	s := &Server{
		hc: opts.HealthChecker,
	}

	// Init the object
	err := s.init()
	if err != nil {
		return nil, err
	}

	return s, nil
}

// Init the Server object and create the mux
func (s *Server) init() error {
	// Init the app server
	err := s.initAppServer()
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) initAppServer() (err error) {
	cfg := config.Get()

	// Create the mux
	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/status/{recordname}", func(w http.ResponseWriter, r *http.Request) {
		recordName := r.PathValue("recordname")
		if recordName == "" {
			errStatusRecordNameEmpty.WriteResponse(r.Context(), w)
			return
		}

		status := s.hc.GetDomainStatus(recordName)
		if status == nil {
			errStatusDomainNotFound.WriteResponse(r.Context(), w)
			return
		}

		respondWithJSON(r.Context(), w, status)
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		respondWithJSON(r.Context(), w, s.hc.GetAllDomainsStatus())
	})

	// Add static files (includes dashboard)
	err = registerStatic(mux)
	if err != nil {
		return fmt.Errorf("failed to register static server: %w", err)
	}

	middlewares := make([]Middleware, 0, 4)
	middlewares = append(middlewares,
		// Recover from panics
		sloghttp.Recovery,
		// Limit request body to 1KB
		MiddlewareMaxBodySize(1<<10),
	)

	if cfg.Dev.EnableCORS {
		middlewares = append(middlewares,
			// CORS
			cors.Default().Handler,
		)
	}

	middlewares = append(middlewares,
		// Log requests
		sloghttp.New(slog.Default()),
	)

	// Add middlewares
	s.handler = Use(mux, middlewares...)

	return nil
}

// Run the web server
// Note this function is blocking, and will return only when the server is shut down via context cancellation.
func (s *Server) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("server is already running")
	}
	defer s.running.Store(false)
	defer s.wg.Wait()

	// App server
	s.wg.Add(1)
	err := s.startAppServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to start app server: %w", err)
	}
	defer func() {
		// Handle graceful shutdown
		defer s.wg.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := s.appSrv.Shutdown(shutdownCtx)
		shutdownCancel()
		if err != nil {
			// Log the error only (could be context canceled)
			slog.WarnContext(shutdownCtx,
				"App server shutdown error",
				slog.Any("error", err),
			)
		}
	}()

	// Block until the context is canceled
	<-ctx.Done()

	// Servers are stopped with deferred calls
	return nil
}

func (s *Server) startAppServer(ctx context.Context) error {
	cfg := config.Get()

	// Create the HTTP(S) server
	s.appSrv = &http.Server{
		Addr:              net.JoinHostPort(cfg.Server.Bind, strconv.Itoa(cfg.Server.Port)),
		MaxHeaderBytes:    1 << 20,
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           s.handler,
	}

	// Create the listener if we don't have one already
	if s.appListener == nil {
		var err error
		s.appListener, err = net.Listen("tcp", s.appSrv.Addr)
		if err != nil {
			return fmt.Errorf("failed to create TCP listener: %w", err)
		}
	}

	// Start the HTTP(S) server in a background goroutine
	slog.InfoContext(ctx, "App server started",
		slog.String("bind", cfg.Server.Bind),
		slog.Int("port", cfg.Server.Port),
	)
	go func() {
		defer s.appListener.Close()

		// Next call blocks until the server is shut down
		srvErr := s.appSrv.Serve(s.appListener)
		if !errors.Is(srvErr, http.ErrServerClosed) {
			utils.FatalError(slog.Default(), "Error starting app server", srvErr)
		}
	}()

	return nil
}

func respondWithJSON(ctx context.Context, w http.ResponseWriter, data any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	err := enc.Encode(data)
	if err != nil {
		slog.WarnContext(ctx, "Error writing JSON response", slog.Any("error", err))
	}
}

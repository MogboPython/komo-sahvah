package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MogboPython/komo-sahvah/backend/internal/config"
	"github.com/MogboPython/komo-sahvah/backend/internal/deployment"
	"github.com/MogboPython/komo-sahvah/backend/internal/repository"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	if err := config.ConnectMongoDB(); err != nil {
		logger.Error("failed to connect to MongoDB", "error", err)
		os.Exit(1)
	}
	logger.Info("connected to MongoDB", "database", config.MongoDB.Name())

	deployRepo := repository.NewDeploymentRepository(config.MongoDB)
	deployHandler := deployment.NewHandler(deployRepo, logger)

	mux := http.NewServeMux()
	registerRoutes(mux, deployHandler)

	port := config.GetEnvOrDefault("PORT", "8080")
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: withLogging(logger, withCORS(mux)),

		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		logger.Error("failed to bind", "addr", srv.Addr, "error", err)
		os.Exit(1)
	}
	logger.Info("server listening", "addr", fmt.Sprintf("http://localhost%s", srv.Addr))

	// Graceful shutdown on SIGINT / SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-quit
	logger.Info("shutdown signal received", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("forced shutdown", "error", err)
	} else {
		logger.Info("server stopped cleanly")
	}
}

// Route table:
//
//	POST   /api/deploy         → create a new deployment (JSON or multipart)
//	GET    /api/deploy/{id}    → get deployment status
//	GET    /api/deploy/{id}/logs → stream build+deploy logs via SSE  (TODO next)
//	GET    /api/healthz        → liveness probe
func registerRoutes(mux *http.ServeMux, h *deployment.Handler) {
	mux.HandleFunc("/api/deploy", h.Create)
	mux.HandleFunc("/api/deploy/", func(w http.ResponseWriter, r *http.Request) {
		// TODO: Route sub-paths: /api/deploy/{id}  and  /api/deploy/{id}/logs
		if isLogsPath(r.URL.Path) {
			// Placeholder — wired up in the next step when I add SSE.
			http.Error(w, "log streaming not yet implemented", http.StatusNotImplemented)
			return
		}
		h.GetStatus(w, r)
	})
	mux.HandleFunc("/api/healthcheck", healthcheck)
}

func isLogsPath(path string) bool {
	const suffix = "/logs"
	return len(path) > len(suffix) && path[len(path)-len(suffix):] == suffix
}

func healthcheck(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// -----------------------------------------------------------------
// Middleware
// -----------------------------------------------------------------

func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/subconscious-labs/deepseek-mcp/internal/config"
	"github.com/subconscious-labs/deepseek-mcp/internal/deepseek"
)

type Server struct {
	client *deepseek.Client
	cfg    config.Config
	logger *slog.Logger
	srv    *sdkmcp.Server
}

func NewServer(client *deepseek.Client, cfg config.Config, logger *slog.Logger) *Server {
	s := &Server{client: client, cfg: cfg, logger: logger}

	srv := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "deepseek-mcp",
		Version: "0.2.0",
	}, nil)

	s.registerTools(srv)
	s.srv = srv
	return s
}

func (s *Server) registerTools(srv *sdkmcp.Server) {
	addSearchTool(srv, s.client, s.cfg, s.logger)
	addFetchTool(srv, s.client, s.cfg, s.logger)
}

func (s *Server) Run(ctx context.Context) error {
	switch s.cfg.Transport {
	case "stdio", "":
		return s.srv.Run(ctx, &sdkmcp.StdioTransport{})
	case "http":
		return s.runHTTP(ctx)
	default:
		return fmt.Errorf("unknown transport %q", s.cfg.Transport)
	}
}

// HTTPHandler возвращает готовый http.Handler с bearer auth.
// Экспортируется, чтобы тесты могли подключить его к httptest без запуска порта.
func (s *Server) HTTPHandler() http.Handler {
	inner := sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return s.srv },
		nil,
	)
	mux := http.NewServeMux()
	mux.Handle("/mcp", s.withAuth(inner))
	// /healthz без auth: liveness probe не должен зависеть от токена.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func (s *Server) runHTTP(ctx context.Context) error {
	if s.cfg.HTTPToken == "" && !s.cfg.AllowInsecureHTTP {
		return errors.New("http transport requires --http-token (or --http-allow-insecure, unsafe)")
	}
	if s.cfg.HTTPToken == "" {
		s.logger.Warn("http transport running WITHOUT bearer auth; --http-allow-insecure is set")
	}

	srv := &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           s.HTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	s.logger.Info("http transport listening",
		"addr", s.cfg.HTTPAddr,
		"path", "/mcp",
		"auth", s.cfg.HTTPToken != "",
	)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	return nil
}

// withAuth оборачивает handler проверкой Bearer-токена.
// ConstantTimeCompare — чтобы не утекал токен по таймингам.
func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.cfg.HTTPToken == "" {
		return next
	}
	want := []byte(s.cfg.HTTPToken)
	const prefix = "Bearer "

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, prefix) {
			unauthorized(w)
			return
		}
		got := []byte(strings.TrimPrefix(h, prefix))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="deepseek-mcp"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

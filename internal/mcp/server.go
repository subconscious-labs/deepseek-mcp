package mcp

import (
	"context"
	"log/slog"

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
		Version: "0.1.0",
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
	return s.srv.Run(ctx, &sdkmcp.StdioTransport{})
}

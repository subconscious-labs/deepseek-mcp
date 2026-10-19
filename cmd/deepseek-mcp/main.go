package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/subconscious-labs/deepseek-mcp/internal/config"
    "github.com/subconscious-labs/deepseek-mcp/internal/deepseek"
    "github.com/subconscious-labs/deepseek-mcp/internal/logging"
    "github.com/subconscious-labs/deepseek-mcp/internal/mcp"
)

var (
    version   = "dev"
    commit    = "unknown"
    buildTime = "unknown"
)

func main() {
    cfg, err := config.Load()
    if err != nil {
        fmt.Fprintf(os.Stderr, "config error: %v\n", err)
        os.Exit(1)
    }

    if cfg.PrintVersion {
        fmt.Printf("deepseek-mcp %s\n", version)
        return
    }

    logger := logging.New(cfg.LogLevel, cfg.LogFormat)
    slog.SetDefault(logger)

    if cfg.Check {
        runCheck(cfg, logger)
        return
    }

    client := deepseek.NewClient(cfg)
    server := mcp.NewServer(client, cfg, logger)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    if err := server.Run(ctx); err != nil {
        logger.Error("server exited", "error", err)
        os.Exit(1)
    }
}

func runCheck(cfg config.Config, logger *slog.Logger) {
    logger.Info("config valid", "transport", cfg.Transport, "api_base", cfg.APIBase)
    if cfg.APIKey == "" {
        logger.Warn("DEEPSEEK_API_KEY not set; --check passed but live calls will fail")
    }
}

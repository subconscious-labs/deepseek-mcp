package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/subconscious-labs/deepseek-mcp/internal/config"
	"github.com/subconscious-labs/deepseek-mcp/internal/counter"
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
		fmt.Printf("deepseek-mcp %s (commit %s, built %s)\n", version, commit, buildTime)
		return
	}
	if cfg.PrintMCPConfig {
		printMCPConfig(cfg)
		return
	}

	logger := logging.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	if cfg.Check {
		runCheck(cfg, logger)
		return
	}

	// Counter создаётся всегда — даже если лимит 0, он ведёт статистику.
	ctr, err := counter.New(cfg.StateDir, cfg.DailyRequestLimit)
	if err != nil {
		logger.Error("counter init failed", "state_dir", cfg.StateDir, "error", err)
		os.Exit(1)
	}
	snap := ctr.Snapshot()
	logger.Info("counter loaded",
		"date", snap.Date,
		"used_today", snap.SearchRequests+snap.FetchRequests,
		"daily_limit", cfg.DailyRequestLimit,
	)

	client := deepseek.NewClientWithOptions(cfg, deepseek.WithCounter(ctr))
	server := mcp.NewServer(client, cfg, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx); err != nil {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func runCheck(cfg config.Config, logger *slog.Logger) {
	logger.Info("config valid",
		"version", version,
		"transport", cfg.Transport,
		"api_base", cfg.APIBase,
		"search_default_count", cfg.SearchDefaultCount,
		"search_qps", cfg.SearchQPS,
		"fetch_qps", cfg.FetchQPS,
		"allow_private_ips", cfg.AllowPrivateIPs,
		"daily_request_limit", cfg.DailyRequestLimit,
		"state_dir", cfg.StateDir,
	)
	if cfg.APIKey == "" {
		logger.Warn("DEEPSEEK_API_KEY not set; config is valid but live calls will fail")
	} else {
		logger.Info("DEEPSEEK_API_KEY present; live API test not performed by --check")
	}

	if cfg.DailyRequestLimit > 0 {
		ctr, err := counter.New(cfg.StateDir, cfg.DailyRequestLimit)
		if err != nil {
			logger.Error("counter init failed", "error", err)
			return
		}
		snap := ctr.Snapshot()
		logger.Info("counter state",
			"date", snap.Date,
			"search_requests", snap.SearchRequests,
			"fetch_requests", snap.FetchRequests,
			"estimated_cost_usd", snap.EstimatedCostUSD,
		)
	}
}

func printMCPConfig(cfg config.Config) {
	abs, err := os.Executable()
	if err != nil {
		abs = "deepseek-mcp"
	}

	key := cfg.APIKey
	leaked := false
	if key == "" {
		key = "sk-REPLACE_ME"
	} else {
		leaked = true
	}

	out := map[string]any{
		"mcpServers": map[string]any{
			"deepseek": map[string]any{
				"command": abs,
				"env": map[string]string{
					"DEEPSEEK_API_KEY": key,
				},
			},
		},
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)

	if leaked {
		fmt.Fprintln(os.Stderr, "warning: output contains DEEPSEEK_API_KEY; handle with care")
	}
}

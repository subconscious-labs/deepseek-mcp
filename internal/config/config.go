package config

import (
    "flag"
    "fmt"
    "os"

    "github.com/kelseyhightower/envconfig"
)

type Config struct {
    APIKey      string `envconfig:"DEEPSEEK_API_KEY" required:"true"`
    APIBase     string `envconfig:"DEEPSEEK_API_BASE" default:"https://api.deepseek.com"`
    Transport   string `envconfig:"MCP_TRANSPORT" default:"stdio"`
    HTTPAddr    string `envconfig:"MCP_HTTP_ADDR" default:":8765"`
    HTTPToken   string `envconfig:"MCP_HTTP_TOKEN"`
    AllowInsecureHTTP bool `envconfig:"MCP_HTTP_ALLOW_INSECURE" default:"false"`

    LogLevel  string `envconfig:"LOG_LEVEL" default:"info"`
    LogFormat string `envconfig:"LOG_FORMAT" default:"json"`
    RedactQueries bool `envconfig:"LOG_REDACT_QUERIES" default:"false"`

    SearchDefaultCount int `envconfig:"SEARCH_DEFAULT_COUNT" default:"5"`
    SearchMaxCount     int `envconfig:"SEARCH_MAX_COUNT" default:"50"`
    SearchTimeoutSec   int `envconfig:"SEARCH_TIMEOUT_SEC" default:"30"`
    FetchTimeoutSec    int `envconfig:"FETCH_TIMEOUT_SEC" default:"60"`

    SearchQPS float64 `envconfig:"SEARCH_QPS" default:"4"`
    FetchQPS  float64 `envconfig:"FETCH_QPS" default:"4"`

    AllowPrivateIPs  bool `envconfig:"ALLOW_PRIVATE_IPS" default:"false"`
    MaxResponseBytes int  `envconfig:"MAX_RESPONSE_BYTES" default:"102400"`

    DailyRequestLimit int    `envconfig:"DAILY_REQUEST_LIMIT" default:"0"`
    StateDir          string `envconfig:"STATE_DIR"`

    // CLI-only
    Check          bool
    PrintVersion   bool
    PrintMCPConfig bool
}

func Load() (Config, error) {
    var cfg Config
    if err := envconfig.Process("", &cfg); err != nil {
        return cfg, fmt.Errorf("env: %w", err)
    }

    // CLI flags override env
    flag.StringVar(&cfg.APIKey, "deepseek-api-key", cfg.APIKey, "DeepSeek API key")
    flag.StringVar(&cfg.APIBase, "deepseek-api-base", cfg.APIBase, "DeepSeek API base URL")
    flag.StringVar(&cfg.Transport, "transport", cfg.Transport, "stdio or http")
    flag.StringVar(&cfg.HTTPAddr, "addr", cfg.HTTPAddr, "HTTP listen address")
    flag.StringVar(&cfg.HTTPToken, "http-token", cfg.HTTPToken, "HTTP bearer token")
    flag.BoolVar(&cfg.AllowInsecureHTTP, "http-allow-insecure", cfg.AllowInsecureHTTP, "Allow HTTP without token")
    flag.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug|info|warn|error")
    flag.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "json|text")
    flag.BoolVar(&cfg.RedactQueries, "redact-queries", cfg.RedactQueries, "Log query hashes instead of raw")
    flag.IntVar(&cfg.SearchDefaultCount, "search-default-count", cfg.SearchDefaultCount, "")
    flag.IntVar(&cfg.SearchMaxCount, "search-max-count", cfg.SearchMaxCount, "")
    flag.IntVar(&cfg.SearchTimeoutSec, "search-timeout-sec", cfg.SearchTimeoutSec, "")
    flag.IntVar(&cfg.FetchTimeoutSec, "fetch-timeout-sec", cfg.FetchTimeoutSec, "")
    flag.Float64Var(&cfg.SearchQPS, "search-qps", cfg.SearchQPS, "")
    flag.Float64Var(&cfg.FetchQPS, "fetch-qps", cfg.FetchQPS, "")
    flag.BoolVar(&cfg.AllowPrivateIPs, "allow-private", cfg.AllowPrivateIPs, "Disable SSRF protection")
    flag.IntVar(&cfg.MaxResponseBytes, "max-response-bytes", cfg.MaxResponseBytes, "")
    flag.IntVar(&cfg.DailyRequestLimit, "daily-request-limit", cfg.DailyRequestLimit, "")
    flag.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "")

    flag.BoolVar(&cfg.Check, "check", false, "Validate config and exit")
    flag.BoolVar(&cfg.PrintVersion, "version", false, "Print version and exit")
    flag.BoolVar(&cfg.PrintMCPConfig, "print-mcp-config", false, "Print MCP client config JSON")
    flag.Parse()

    if cfg.StateDir == "" {
        cfg.StateDir = defaultStateDir()
    }

    // Post-validation
    if cfg.SearchDefaultCount < 1 || cfg.SearchDefaultCount > cfg.SearchMaxCount {
        return cfg, fmt.Errorf("search-default-count must be between 1 and %d", cfg.SearchMaxCount)
    }
    if cfg.Transport != "stdio" && cfg.Transport != "http" {
        return cfg, fmt.Errorf("transport must be stdio or http")
    }

    return cfg, nil
}

func defaultStateDir() string {
    if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
        return xdg + "/deepseek-mcp"
    }
    home, _ := os.UserHomeDir()
    return home + "/.local/state/deepseek-mcp"
}

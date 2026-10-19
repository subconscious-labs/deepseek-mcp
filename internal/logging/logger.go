package logging

import (
    "log/slog"
    "os"
)

func New(level, format string) *slog.Logger {
    var lvl slog.Level
    switch level {
    case "debug":
        lvl = slog.LevelDebug
    case "warn":
        lvl = slog.LevelWarn
    case "error":
        lvl = slog.LevelError
    default:
        lvl = slog.LevelInfo
    }

    opts := &slog.HandlerOptions{Level: lvl}
    var handler slog.Handler
    if format == "text" {
        handler = slog.NewTextHandler(os.Stderr, opts)
    } else {
        handler = slog.NewJSONHandler(os.Stderr, opts)
    }
    return slog.New(handler)
}

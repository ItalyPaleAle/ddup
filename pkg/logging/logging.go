package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"

	"github.com/italypaleale/ddup/pkg/buildinfo"
	"github.com/italypaleale/ddup/pkg/config"
)

func getLogLevel(cfg *config.Config) (slog.Level, error) {
	switch strings.ToLower(cfg.Logs.Level) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info": // Also default log level
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, config.NewConfigError("Invalid value for 'logLevel'", "Invalid configuration")
	}
}

func GetLogger(ctx context.Context, cfg *config.Config) (log *slog.Logger, shutdownFn func(ctx context.Context) error, err error) {
	// Get the level
	level, err := getLogLevel(cfg)
	if err != nil {
		return nil, nil, err
	}

	// Create the handler
	var handler slog.Handler
	switch {
	case cfg.Logs.JSON:
		// Log as JSON if configured
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	case isatty.IsTerminal(os.Stdout.Fd()):
		// Enable colors if we have a TTY
		handler = tint.NewHandler(os.Stdout, &tint.Options{
			Level:      level,
			TimeFormat: time.StampMilli,
		})
	default:
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		})
	}

	log = slog.New(handler).
		With(slog.String("app", buildinfo.AppName)).
		With(slog.String("version", buildinfo.AppVersion))

	return log, shutdownFn, nil
}

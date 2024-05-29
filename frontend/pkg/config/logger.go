package config

import (
	"log/slog"
	"os"
)

func DefaultLogger() *slog.Logger {
	handlerOptions := slog.HandlerOptions{}
	handler := slog.NewJSONHandler(os.Stdout, &handlerOptions)
	logger := slog.New(handler)
	return logger
}

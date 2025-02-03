package util

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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

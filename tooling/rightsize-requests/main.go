// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/internal/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := createLogger(0)
	ctx = logr.NewContext(ctx, logger)

	// Default to `dev` credentials so `az login` works out of the box for local
	// runs; the command requires AZURE_TOKEN_CREDENTIALS to be set.
	if os.Getenv("AZURE_TOKEN_CREDENTIALS") == "" {
		if err := os.Setenv("AZURE_TOKEN_CREDENTIALS", "dev"); err != nil {
			logger.Error(err, "failed to set default AZURE_TOKEN_CREDENTIALS")
			os.Exit(1)
		}
	}

	root := cmd.NewRootCommand()
	if err := root.ExecuteContext(ctx); err != nil {
		logger.Error(err, "command failed")
		os.Exit(1)
	}
}

func createLogger(verbosity int) logr.Logger {
	handler := prettylog.NewHandler(&slog.HandlerOptions{
		Level:       slog.Level(verbosity * -1),
		AddSource:   false,
		ReplaceAttr: nil,
	})
	return logr.FromSlogHandler(handler)
}

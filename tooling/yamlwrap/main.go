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
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/dusted-go/logging/prettylog"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/yamlwrap"
)

func main() {
	logger := createLogger(0)
	var logVerbosity int
	// Create a root context with the logger and signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rootCmd := &cobra.Command{
		Use:   "yaml-template-wrapper",
		Short: "Wrap and unwrap Go template expressions in YAML files for yamlfmt compatibility",
		Long: `A tool to temporarily wrap Go template expressions in YAML files with quoted strings
and metadata comments, allowing them to be processed by yamlfmt and other YAML tools.

The wrap operation converts template expressions like:
  count: {{ .value }}
Into:
  count: "{{ .value }}" # __WRAPPED_TEMPLATE__

The unwrap operation reverses this transformation, restoring the original template syntax.`,
		SilenceUsage: true,
	}

	// Add subcommands
	commands := []func() (*cobra.Command, error){
		yamlwrap.NewWrapCommand,
		yamlwrap.NewUnwrapCommand,
	}
	for _, newCmd := range commands {
		c, err := newCmd()
		if err != nil {
			slog.Error("failed to add command", "error", err)
			os.Exit(1)
		}
		// The upstream directory walker has no exclusions. Keep dependency
		// sources untouched while reusing upstream file transformations.
		runFile := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			dir, err := cmd.Flags().GetString("dir")
			if err != nil {
				return err
			}
			if dir == "" {
				return runFile(cmd, args)
			}
			skipValidation, err := cmd.Flags().GetBool("no-validate-result")
			if err != nil {
				return err
			}
			return processDirectory(dir, cmd.Name() == "wrap", !skipValidation)
		}
		rootCmd.AddCommand(c)
	}

	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		ctx = logr.NewContext(ctx, createLogger(logVerbosity))
		cmd.SetContext(ctx)
	}
	rootCmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	if err := rootCmd.Execute(); err != nil {
		logger.Error(err, "Command failed.")
		os.Exit(1)
	}
}

func processDirectory(dir string, wrap, validate bool) error {
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "vendor", ".git", ".bingo":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		if wrap {
			return yamlwrap.WrapFile(path, path, validate)
		}
		return yamlwrap.UnwrapFile(path, path)
	})
}

func createLogger(verbosity int) logr.Logger {
	prettyHandler := prettylog.NewHandler(&slog.HandlerOptions{
		Level:       slog.Level(verbosity * -1),
		AddSource:   false,
		ReplaceAttr: nil,
	})
	return logr.FromSlogHandler(prettyHandler)
}

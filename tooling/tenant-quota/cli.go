// Copyright 2026 Microsoft Corporation
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
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/cijoboutcomes"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

// Only main's no-argument path may start the daemon.
func runCommand(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	if len(args) > 0 && (args[0] == "ci-outcomes" || args[0] == "ci-discovery") {
		return runCICommand(ctx, args[0], args[1:], stdout, stderr, logger)
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "-help") {
		_, err := fmt.Fprintln(stderr, "Usage: tenant-quota-collector [ci-outcomes|ci-discovery OPTIONS]\n\nNo arguments: run the service using CONFIG_PATH.\nci-outcomes: process exact GCS job URIs (read-only by default).\nci-discovery: bounded GCS discovery (read-only by default).\nUse COMMAND --help for selection and output options.")
		return err
	}
	return fmt.Errorf("unknown command or arguments %q; use --help", args)
}

func parseCIOutcomes(args []string, stderr io.Writer) (string, cijoboutcomes.OneShotOptions, error) {
	return parseCICommand("ci-outcomes", args, stderr)
}

func parseCICommand(command string, args []string, stderr io.Writer) (string, cijoboutcomes.OneShotOptions, error) {
	options := cijoboutcomes.OneShotOptions{Discovery: command == "ci-discovery"}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "runtime YAML config (required; tenants are not needed)")
	if options.Discovery {
		flags.Func("job-name", "exact job name (repeatable; default: all configured matching jobs)", func(name string) error {
			options.JobNames = append(options.JobNames, name)
			return nil
		})
		flags.BoolVar(&options.AllHistory, "all-history", false, "list all supported history; excludes --since; requires --until")
		flags.BoolVar(&options.Bulk, "bulk", false, "submit all selected discovery rows as one batch; requires --all-history; excludes --limit")
		for _, bound := range []struct {
			name string
			dst  *time.Time
			help string
		}{
			{"since", &options.Since, "inclusive build snowflake time (RFC3339; required unless --all-history)"},
			{"until", &options.Until, "inclusive fixed upper bound (RFC3339; required)"},
		} {
			flags.Func(bound.name, bound.help, func(value string) error {
				parsed, err := time.Parse(time.RFC3339, value)
				*bound.dst = parsed
				return err
			})
		}
		flags.IntVar(&options.Limit, "limit", 0, "positive cap after canonical URI deduplication and ascending build ID sort, URI tie-break (required unless --bulk)")
	} else {
		flags.Func("job-uri", "exact gs:// job artifact root (required; repeatable)", func(raw string) error {
			uri, err := cijoboutcomes.CanonicalJobURI(raw)
			if err == nil {
				options.JobURIs = append(options.JobURIs, uri)
			}
			return err
		})
		flags.BoolVar(&options.InspectArtifacts, "inspect-artifacts", false, "read-only artifact inspection; ignore tags and bypass Kusto/Azure credentials")
	}
	flags.IntVar(&options.Workers, "workers", 0, "positive worker count (default: runtime config)")
	dryRun := flags.Bool("dry-run", true, "emit JSONL without ingestion (default)")
	flags.BoolVar(&options.Ingest, "ingest", false, "submit missing batches to Kusto")
	if err := flags.Parse(args); err != nil {
		return "", options, err
	}
	if *path == "" || flags.NArg() != 0 {
		return "", options, errors.New("--config is required; positional arguments are not supported")
	}
	var flagErr error
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "dry-run":
			if !*dryRun || options.Ingest {
				flagErr = errors.New("use either --dry-run or --ingest, not both; --dry-run=false is not supported")
			}
		case "workers":
			if options.Workers <= 0 {
				flagErr = errors.New("--workers must be positive")
			}
		case "limit":
			if options.Limit <= 0 || options.Bulk {
				flagErr = errors.New("--limit must be positive and cannot be combined with --bulk")
			}
		case "since":
			if options.AllHistory {
				flagErr = errors.New("--since cannot be combined with --all-history")
			}
		}
	})
	return *path, options, errors.Join(flagErr, options.Validate())
}

func runCIOutcomes(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	return runCICommand(ctx, "ci-outcomes", args, stdout, stderr, logger)
}

func runCICommand(ctx context.Context, command string, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	path, options, err := parseCICommand(command, args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	cfg, err := config.LoadCIJobOutcomesFromFile(path)
	if err != nil {
		return fmt.Errorf("load CI outcomes config: %w", err)
	}
	configurePanicHandling(cfg.ExitOnPanic)
	klog.SetLogger(logr.FromSlogHandler(logger.Handler()))
	return cijoboutcomes.NewWriter(cfg, logger).RunOnce(ctx, options, stdout)
}

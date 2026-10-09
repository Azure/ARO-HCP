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
	if len(args) > 0 && args[0] == "ci-outcomes" {
		return runCIOutcomes(ctx, args[1:], stdout, stderr, logger)
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "-help") {
		_, err := fmt.Fprintln(stderr, "Usage: tenant-quota-collector [ci-outcomes OPTIONS]\n\nNo arguments: run the service using CONFIG_PATH.\nci-outcomes: bounded one-shot CI collection (dry-run by default).\nUse ci-outcomes --help for selection and output options.")
		return err
	}
	return fmt.Errorf("unknown command or arguments %q; use --help", args)
}

func parseCIOutcomes(args []string, stderr io.Writer) (string, cijoboutcomes.OneShotOptions, error) {
	var options cijoboutcomes.OneShotOptions
	flags := flag.NewFlagSet("ci-outcomes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "runtime YAML config (required; tenants are not needed)")
	flags.Func("build-id", "exact build ID (repeatable; excludes interval selection)", func(id string) error {
		options.BuildIDs = append(options.BuildIDs, cijoboutcomes.BuildID(id))
		return nil
	})
	flags.Func("release", "Sippy release (repeatable; requires since, until, limit)", func(release string) error {
		options.Releases = append(options.Releases, release)
		return nil
	})
	for _, bound := range []struct {
		name string
		dst  *time.Time
		help string
	}{
		{"since", &options.Since, "inclusive job START time (RFC3339)"},
		{"until", &options.Until, "exclusive job START time (RFC3339)"},
	} {
		flags.Func(bound.name, bound.help, func(value string) error {
			parsed, err := time.Parse(time.RFC3339, value)
			*bound.dst = parsed
			return err
		})
	}
	flags.IntVar(&options.Limit, "limit", 0, "positive cap after sorting by job start and build ID")
	flags.IntVar(&options.Workers, "workers", 0, "positive worker count (default: runtime config)")
	dryRun := flags.Bool("dry-run", true, "read live tags and emit missing payloads without ingestion (default)")
	flags.BoolVar(&options.Ingest, "ingest", false, "submit missing batches to Kusto")
	flags.BoolVar(&options.InspectArtifacts, "inspect-artifacts", false, "read-only artifact inspection; ignore tags and bypass Kusto/Azure credentials")
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
		case "release", "since", "until", "limit":
			if len(options.BuildIDs) > 0 {
				flagErr = errors.New("--build-id cannot be combined with release/time/limit selection")
			}
		}
	})
	return *path, options, errors.Join(flagErr, options.Validate())
}

func runCIOutcomes(ctx context.Context, args []string, stdout, stderr io.Writer, logger *slog.Logger) error {
	path, options, err := parseCIOutcomes(args, stderr)
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

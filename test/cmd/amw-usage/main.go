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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/test/util/amwusage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type stringFlags []string

func (s *stringFlags) String() string         { return strings.Join(*s, ", ") }
func (s *stringFlags) Set(value string) error { *s = append(*s, value); return nil }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runWithCollector(ctx, args, stdout, stderr, amwusage.Collect)
}

func runWithCollector(ctx context.Context, args []string, stdout, stderr io.Writer, collect func(context.Context, amwusage.CollectOptions) (amwusage.CollectSummary, error)) error {
	if len(args) == 0 {
		return errors.New("usage: amw-usage collect|scan|refresh|recover-namespaces|enrich|labels|status|compact|repair|render [flags]; use <command> --help")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	var input, output string
	parse := func() error {
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected positional arguments")
		}
		return nil
	}
	switch args[0] {
	case "labels":
		var database string
		var azureCLI, migrateOnly bool
		var workers int
		var limits amwusage.LabelScanOptions
		flags.StringVar(&database, "db", "", "isolated SQLite backup of the full-run ranking snapshot")
		flags.BoolVar(&azureCLI, "azure-cli", false, "use SDK AzureCLICredential")
		flags.BoolVar(&migrateOnly, "migrate-only", false, "normalize an isolated legacy backup offline; never send cloud requests")
		flags.IntVar(&workers, "workers", 2, "maximum concurrent label requests, 1-2")
		flags.Int64Var(&limits.MaxBytes, "max-bytes", 2147483648, "cumulative response byte budget; interrupted attempts reserve 32 MiB")
		flags.IntVar(&limits.MaxRequests, "max-requests", 10000, "cumulative physical attempt budget, including retries")
		if err := parse(); err != nil {
			return helpError(err)
		}
		if database == "" || workers < 1 || workers > 2 || limits.MaxBytes < 33554433 || limits.MaxRequests < 1 {
			return errors.New("labels requires --db, workers 1-2, max-bytes >= 33554433 and positive max-requests")
		}
		if _, err := os.Stat(database); err != nil {
			return err
		}
		store, err := amwusage.OpenScanStore(database)
		if err != nil {
			return err
		}
		defer store.Close()
		if migrateOnly {
			if err := store.MigrateLabels(ctx); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "Label normalization migration complete; collection remains paused. Resume labels when ready; compact only after all work is terminal.")
			return nil
		}
		if err := store.PlanLabels(ctx, limits); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Full-run raw labels: %s; cumulative limits %d bytes / %d requests; %d workers. Resume with this command.\n", database, limits.MaxBytes, limits.MaxRequests, workers)
		var credential azcore.TokenCredential
		if azureCLI {
			credential, err = azidentity.NewAzureCLICredential(nil)
		} else {
			credential, err = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
		}
		if err != nil {
			return err
		}
		scanErr := store.Scan(ctx, amwusage.ScanOptions{Workers: workers, Credential: credential, Kinds: []string{"inventory_labels"}})
		summaryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		summary, summaryErr := store.LabelSummary(summaryCtx)
		if summaryErr == nil {
			summaryErr = json.NewEncoder(stdout).Encode(summary)
			if !summary.CoverageComplete {
				scanErr = errors.Join(scanErr, errors.New("label coverage is partial; inspect label_scan_coverage and query/attempt ledger, increase budgets to resume pending work"))
			}
		}
		return errors.Join(scanErr, summaryErr)
	case "refresh":
		var database string
		var retryEmpty bool
		flags.StringVar(&database, "db", "", "existing SQLite database; stop scanners first")
		flags.StringVar(&input, "input", "", "fresh discovery seed with the original fixed windows and workspaces")
		flags.BoolVar(&retryEmpty, "retry-empty", false, "reopen directly measured empty ranking queries, preserving attempt history")
		if err := parse(); err != nil {
			return helpError(err)
		}
		if database == "" || input == "" {
			return errors.New("refresh requires --db and --input; collect fresh discovery separately with the original windows")
		}
		if _, err := os.Stat(database); err != nil {
			return err
		}
		for _, path := range databaseProtectedPaths(database) {
			if err := checkReportOutput(path, input); err != nil {
				return err
			}
		}
		file, err := os.Open(input)
		if err != nil {
			return err
		}
		defer file.Close()
		store, err := amwusage.OpenScanStore(database)
		if err != nil {
			return err
		}
		defer store.Close()
		if retryEmpty {
			err = store.RefreshCatalogRetryEmpty(ctx, file)
		} else {
			err = store.RefreshCatalog(ctx, file)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Catalog refreshed offline; original fingerprint and accepted nonempty results preserved. Resume with scan --db. For full remeasurement use a fresh database.")
		summary, err := store.Summary(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(summary)
	case "recover-namespaces":
		var database string
		var azureCLI bool
		var workers int
		flags.StringVar(&database, "db", "", "new SQLite backup of the final ranking snapshot")
		flags.BoolVar(&azureCLI, "azure-cli", false, "use SDK AzureCLICredential")
		flags.IntVar(&workers, "workers", 2, "maximum concurrent requests, 1-2")
		if err := parse(); err != nil {
			return helpError(err)
		}
		if database == "" || workers < 1 || workers > 2 {
			return errors.New("recover-namespaces requires --db and --workers between 1 and 2")
		}
		if _, err := os.Stat(database); err != nil {
			return err
		}
		if err := checkReportOutput(scanReportPath(database), databaseProtectedPaths(database)...); err != nil {
			return err
		}
		store, err := amwusage.OpenScanStore(database)
		if err != nil {
			return err
		}
		defer store.Close()
		children, err := store.PlanNamespaceRecovery(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Namespace recovery: %d newly planned children; durable maximum 1500 attempts. Database: %s\n", children, database)
		fmt.Fprintln(stdout, "Roots without eligible inventory remain waiting_inventory (unknown); rerun recovery after ranking inventory becomes available. Existing plans remain immutable.")
		var credential azcore.TokenCredential
		if azureCLI {
			credential, err = azidentity.NewAzureCLICredential(nil)
		} else {
			credential, err = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
		}
		if err != nil {
			return err
		}
		scanErr := store.Scan(ctx, amwusage.ScanOptions{Workers: workers, Credential: credential, NamespaceOnly: true})
		summaryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		summary, summaryErr := store.Summary(summaryCtx)
		if summaryErr == nil {
			summaryErr = json.NewEncoder(stdout).Encode(summary)
		}
		return finishScanReport(ctx, database, "", errors.Join(scanErr, summaryErr), stdout)
	case "enrich":
		var database, plan string
		var azureCLI bool
		var workers int
		flags.StringVar(&database, "db", "", "existing isolated SQLite snapshot; never the active ranking database")
		flags.StringVar(&plan, "plan", "", "bounded enrichment JSON plan; omitted to resume the stored plan")
		flags.BoolVar(&azureCLI, "azure-cli", false, "use SDK AzureCLICredential")
		flags.IntVar(&workers, "workers", 2, "maximum concurrent enrichment requests, 1-4")
		if err := parse(); err != nil {
			return helpError(err)
		}
		if database == "" {
			return errors.New("enrich requires --db")
		}
		if workers < 1 || workers > 4 {
			return errors.New("--workers must be between 1 and 4")
		}
		if _, err := os.Stat(database); err != nil {
			return err
		}
		if err := checkReportOutput(scanReportPath(database), append(databaseProtectedPaths(database), plan)...); err != nil {
			return err
		}
		if plan != "" {
			if err := checkReportOutput(database, plan); err != nil {
				return err
			}
		}
		store, err := amwusage.OpenScanStore(database)
		if err != nil {
			return err
		}
		defer store.Close()
		if plan != "" {
			file, err := os.Open(plan)
			if err != nil {
				return err
			}
			err = errors.Join(store.PlanEnrichment(ctx, file), file.Close())
			if err != nil {
				return err
			}
		} else if err := store.PlanEnrichment(ctx, nil); err != nil {
			return err
		}
		var credential azcore.TokenCredential
		if azureCLI {
			credential, err = azidentity.NewAzureCLICredential(nil)
		} else {
			credential, err = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
		}
		if err != nil {
			return fmt.Errorf("configure Azure credentials with --azure-cli or AZURE_TOKEN_CREDENTIALS: %w", err)
		}
		scanErr := store.Scan(ctx, amwusage.ScanOptions{Workers: workers, Credential: credential, Kinds: []string{"samples_window", "inventory_samples"}})
		summaryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		summary, summaryErr := store.EnrichmentSummary(summaryCtx)
		fmt.Fprintf(stdout, "Enrichment database: %s\n", database)
		if summaryErr == nil {
			summaryErr = json.NewEncoder(stdout).Encode(summary)
			if !summary.CoverageComplete {
				scanErr = errors.Join(scanErr, errors.New("enrichment coverage is partial; inspect query and attempt ledger"))
			}
		}
		return finishScanReport(ctx, database, plan, errors.Join(scanErr, summaryErr), stdout)
	case "repair":
		var database, parent string
		flags.StringVar(&database, "db", "", "existing SQLite database; stop older scanners first")
		flags.StringVar(&parent, "query", "", "one blocked hard-limit query ID to partition")
		if err := parse(); err != nil {
			return helpError(err)
		}
		if database == "" || parent == "" {
			return errors.New("repair requires --db and --query")
		}
		if _, err := os.Stat(database); err != nil {
			return err
		}
		store, err := amwusage.OpenScanStore(database)
		if err != nil {
			return err
		}
		defer store.Close()
		children, err := store.RepairPartition(ctx, parent)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Planned %d new partition children for %s. No HTTP requests made; resume with scan.\n", children, parent)
		return err
	case "scan", "status", "compact":
		var database string
		var azureCLI bool
		var workers int
		flags.StringVar(&database, "db", "", "durable SQLite database path")
		if args[0] == "scan" {
			flags.StringVar(&input, "input", "", "schema-1 seed JSON, required only for initialization")
			flags.BoolVar(&azureCLI, "azure-cli", false, "use SDK AzureCLICredential")
			flags.IntVar(&workers, "workers", 4, "maximum concurrent requests, 1-4; initially one per workspace")
		}
		if err := parse(); err != nil {
			return helpError(err)
		}
		if database == "" {
			return errors.New("--db is required")
		}
		if args[0] == "status" {
			summary, err := amwusage.ReadScanSummary(ctx, database)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(stdout, "Database: %s\n", database); err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(summary)
		}
		if args[0] == "compact" {
			if err := amwusage.PruneScanStore(ctx, database); err != nil {
				return err
			}
			_, err := fmt.Fprintf(stdout, "Compacted: %s\n", database)
			return err
		}
		if args[0] == "scan" && (workers < 1 || workers > 4) {
			return errors.New("--workers must be between 1 and 4")
		}
		if input == "" {
			if _, err := os.Stat(database); err != nil {
				return fmt.Errorf("existing --db required (initialize with scan --input): %w", err)
			}
		}
		if input != "" {
			if err := checkReportOutput(database, input); err != nil {
				return err
			}
		}
		if args[0] == "scan" {
			if err := checkReportOutput(scanReportPath(database), append(databaseProtectedPaths(database), input)...); err != nil {
				return err
			}
		}
		store, err := amwusage.OpenScanStore(database)
		if err != nil {
			return err
		}
		defer store.Close()
		if input != "" {
			file, err := os.Open(input)
			if err != nil {
				return err
			}
			initializeErr := store.Initialize(ctx, file)
			if err := errors.Join(initializeErr, file.Close()); err != nil {
				return err
			}
		}
		var scanErr error
		if args[0] == "scan" {
			var credential azcore.TokenCredential
			if azureCLI {
				credential, err = azidentity.NewAzureCLICredential(nil)
			} else {
				credential, err = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
			}
			if err != nil {
				return fmt.Errorf("configure Azure credentials with --azure-cli or AZURE_TOKEN_CREDENTIALS: %w", err)
			}
			scanErr = store.Scan(ctx, amwusage.ScanOptions{Workers: workers, Credential: credential})
		}
		summaryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		summary, summaryErr := store.Summary(summaryCtx)
		fmt.Fprintf(stdout, "Database: %s\n", database)
		if summaryErr == nil {
			summaryErr = json.NewEncoder(stdout).Encode(summary)
		}
		if args[0] == "scan" {
			return finishScanReport(ctx, database, input, errors.Join(scanErr, summaryErr), stdout)
		}
		return summaryErr
	case "collect":
		var workspaces, metrics, inventoryMetrics stringFlags
		var start, end, selection, inventorySelection, contextFile, runName, prowURL string
		var azureCLI bool
		flags.Var(&workspaces, "workspace", "Microsoft.Monitor/accounts ARM ID (repeatable, at most 4)")
		flags.Var(&metrics, "metric", "exact metric name, matched per workspace (repeatable; omitted means no PromQL)")
		flags.Var(&inventoryMetrics, "inventory-metric", "full physical label inventories for an exact metric at start/end over preceding 12h (repeatable; at most 3 workspace/metric pairs, 6 requests, 32 MiB per response)")
		flags.StringVar(&start, "start", "", "required RFC3339 start; window 5 minutes to 12 hours")
		flags.StringVar(&end, "end", "", "required RFC3339 end")
		flags.StringVar(&selection, "selection", "", "optional JSON map of workspace name or ARM ID to exact metric names")
		flags.StringVar(&inventorySelection, "inventory-selection", "", "optional JSON map of workspace name or ARM ID to exact inventory metric names (same total cap as --inventory-metric)")
		flags.StringVar(&contextFile, "context", "", "optional schema-1 ownership and explicit baseline JSON; sources are not fetched or verified")
		flags.StringVar(&runName, "run-name", "", "optional user-provided run label")
		flags.StringVar(&prowURL, "prow-url", "", "optional HTTPS annotation; NOT fetched or verified")
		flags.StringVar(&output, "output", "", "required new evidence JSON path; also generates adjacent same-stem .html")
		flags.BoolVar(&azureCLI, "azure-cli", false, "use SDK AzureCLICredential instead of AZURE_TOKEN_CREDENTIALS selection")
		if err := parse(); err != nil {
			return helpError(err)
		}
		startTime, err := time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return fmt.Errorf("--start: %w", err)
		}
		endTime, err := time.Parse(time.RFC3339Nano, end)
		if err != nil {
			return fmt.Errorf("--end: %w", err)
		}
		o := amwusage.CollectOptions{Workspaces: workspaces, Metrics: metrics, InventoryMetrics: inventoryMetrics, Start: startTime, End: endTime, RunName: runName, ProwURL: prowURL, Output: output}
		if selection != "" {
			data, err := os.ReadFile(selection)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &o.Selection); err != nil {
				return fmt.Errorf("selection: %w", err)
			}
			if o.Selection == nil {
				return errors.New("selection must be a JSON object")
			}
		}
		if inventorySelection != "" {
			data, err := os.ReadFile(inventorySelection)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &o.InventorySelection); err != nil {
				return fmt.Errorf("inventory-selection: %w", err)
			}
			if o.InventorySelection == nil {
				return errors.New("inventory-selection must be a JSON object")
			}
		}
		if contextFile != "" {
			o.Context, err = os.ReadFile(contextFile)
			if err != nil {
				return fmt.Errorf("context: %w", err)
			}
			if len(o.Context) == 0 {
				return errors.New("context file must not be empty")
			}
		}
		if err := o.Validate(); err != nil {
			return err
		}
		// Refuse existing evidence before invoking the collector, including dangling
		// symlinks. Collect independently enforces exclusive creation as well.
		if _, err := os.Lstat(output); err == nil {
			return fmt.Errorf("evidence output already exists: %s", output)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		report := strings.TrimSuffix(output, filepath.Ext(output)) + ".html"
		if err := checkReportOutput(report, output, selection, inventorySelection, contextFile); err != nil {
			return err
		}
		var credential azcore.TokenCredential
		if azureCLI {
			credential, err = azidentity.NewAzureCLICredential(nil)
		} else {
			credential, err = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
		}
		if err != nil {
			return fmt.Errorf("configure Azure credentials with --azure-cli or AZURE_TOKEN_CREDENTIALS: %w", err)
		}
		o.Credential = credential
		summary, collectErr := collect(ctx, o)
		fmt.Fprintf(stdout, "Collection: %d requests, %d failed, %d platform, %d PromQL; %d selected workspace/metric pairs.\nEvidence: %s\n", summary.Requests, summary.Failed, summary.Platform, summary.PromQL, summary.Selected, output)
		if ctx.Err() != nil || errors.Is(collectErr, context.Canceled) || errors.Is(collectErr, context.DeadlineExceeded) {
			_, writeErr := fmt.Fprintf(stdout, "Automatic HTML skipped after cancellation. Render offline: amw-usage render --input %q --output %q\n", output, report)
			return errors.Join(collectErr, ctx.Err(), writeErr)
		}
		// A failed collection can still have useful checkpoints. Render those without
		// replacing the original error or claiming that partial evidence is complete.
		renderErr := renderReport(output, report, selection, inventorySelection, contextFile)
		if renderErr != nil {
			renderErr = fmt.Errorf("HTML generation failed: %w", renderErr)
		}
		fmt.Fprintf(stdout, "HTML: %s\n", report)
		return errors.Join(collectErr, renderErr)
	case "render":
		flags.StringVar(&input, "input", "", "schema-1 JSON or SQLite scan database (offline; format auto-detected)")
		flags.StringVar(&output, "output", "", "standalone HTML path (replaced safely; must differ from input)")
		if err := parse(); err != nil {
			return helpError(err)
		}
		if input == "" || output == "" {
			return errors.New("--input and --output are required")
		}
		if err := renderReport(input, output); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Rendered %s (offline)\n", output)
		return nil
	case "--help", "-h", "help":
		fmt.Fprintln(stdout, "usage: amw-usage collect|scan|refresh|recover-namespaces|enrich|labels|status|compact|repair|render [flags]; use <command> --help")
		return nil
	default:
		return fmt.Errorf("unknown command %q; expected collect, scan, refresh, recover-namespaces, enrich, labels, status, compact, repair or render", args[0])
	}
}

func renderReport(input, output string, protected ...string) error {
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	var header [16]byte
	_, readErr := io.ReadFull(file, header[:])
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if string(header[:]) == "SQLite format 3\x00" {
		protected = append(protected, databaseProtectedPaths(input)...)
		return replaceReport(input, output, func(w io.Writer) error { return amwusage.RenderDatabase(w, input) }, protected...)
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	return replaceReport(input, output, func(w io.Writer) error { return amwusage.Render(w, data) }, protected...)
}

func scanReportPath(database string) string {
	return strings.TrimSuffix(database, filepath.Ext(database)) + ".html"
}

func databaseProtectedPaths(database string) []string {
	paths := []string{database, database + "-wal", database + "-shm", database + "-journal"}
	if resolved, err := filepath.EvalSymlinks(database); err == nil {
		paths = append(paths, resolved, resolved+"-wal", resolved+"-shm", resolved+"-journal")
	}
	return paths
}

func finishScanReport(ctx context.Context, database, input string, scanErr error, stdout io.Writer) error {
	report := scanReportPath(database)
	if ctx.Err() != nil || errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded) {
		_, writeErr := fmt.Fprintf(stdout, "Automatic HTML skipped after cancellation. Render offline: amw-usage render --input %q --output %q\n", database, report)
		return errors.Join(scanErr, ctx.Err(), writeErr)
	}
	renderErr := renderReport(database, report, input)
	if renderErr != nil {
		return errors.Join(scanErr, fmt.Errorf("HTML generation failed: %w", renderErr))
	}
	_, writeErr := fmt.Fprintf(stdout, "HTML: %s\n", report)
	return errors.Join(scanErr, writeErr)
}

// Protected paths may not exist yet during collection preflight. Resolve their
// parent directories as well as comparing existing file identities.
func checkReportOutput(output string, protected ...string) error {
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(outputPath))
	if err != nil {
		return err
	}
	outputPath = filepath.Join(parent, filepath.Base(outputPath))
	outputInfo, err := os.Stat(outputPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		if info, linkErr := os.Lstat(outputPath); linkErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("report output must not be a dangling symlink")
		} else if linkErr != nil && !errors.Is(linkErr, os.ErrNotExist) {
			return linkErr
		}
	}
	if outputInfo != nil && !outputInfo.Mode().IsRegular() {
		return errors.New("report output must be a regular file")
	}
	for _, input := range protected {
		if input == "" {
			continue
		}
		inputPath, err := filepath.Abs(input)
		if err != nil {
			return err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(inputPath))
		if err != nil {
			return err
		}
		inputPath = filepath.Join(parent, filepath.Base(inputPath))
		inputInfo, err := os.Stat(inputPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if inputPath == outputPath || (runtime.GOOS == "windows" && strings.EqualFold(inputPath, outputPath)) || (inputInfo != nil && outputInfo != nil && os.SameFile(inputInfo, outputInfo)) {
			return errors.New("report output must not overwrite or alias input evidence, selection, or context (including symlinks and hardlinks)")
		}
	}
	return nil
}

func replaceReport(input, output string, write func(io.Writer) error, protected ...string) error {
	protected = append([]string{input}, protected...)
	if err := checkReportOutput(output, protected...); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".amw-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	writeErr := write(file)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if err := checkReportOutput(output, protected...); err != nil {
		return err
	}
	// Publish only the closed, complete report; never delete the old one first.
	return os.Rename(file.Name(), output)
}

func helpError(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

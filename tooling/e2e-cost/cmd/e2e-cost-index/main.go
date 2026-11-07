// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	cost "github.com/Azure/ARO-HCP/tooling/e2e-cost"
)

func main() {
	os.Exit(run())
}

func run() int {
	day := flag.String("day", "", "UTC day the runs started, YYYY-MM-DD (required)")
	snapshots := flag.String("snapshots", "", "Directory holding the day's <build>.json snapshots (required)")
	dir := flag.String("dir", "", "Site directory for the day; receives index.json beside the <build>.json.gz reports (required)")
	manifestPath := flag.String("manifest", "manifest.json", "Site manifest to update; created if missing")
	siteIndex := flag.String("site-index", "index.html", "Site viewer HTML path")
	flag.Parse()
	if *day == "" || *dir == "" || *snapshots == "" || flag.NArg() != 0 {
		flag.Usage()
		return 2
	}
	if _, err := time.Parse(time.DateOnly, *day); err != nil {
		flag.Usage()
		return 2
	}
	paths, err := filepath.Glob(filepath.Join(*snapshots, "*.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "List snapshots: %v\n", err)
		return 1
	}
	var runs []cost.DayRun
	for _, path := range paths {
		snapshot, err := cost.ReadSnapshot(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Read %s: %v\n", path, err)
			return 1
		}
		runs = append(runs, cost.SummarizeRun(snapshot, strings.TrimSuffix(filepath.Base(path), ".json")+".json.gz"))
	}
	if err := cost.WriteFile(filepath.Join(*dir, "index.json"), func(w io.Writer) error { return cost.WriteDayIndex(w, *day, runs) }); err != nil {
		fmt.Fprintf(os.Stderr, "Write day index: %v\n", err)
		return 1
	}
	manifest, err := cost.ReadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Manifest: %v\n", err)
		return 1
	}
	manifest.Upsert(cost.SummarizeDay(*day, runs))
	if err := cost.WriteFile(*manifestPath, func(w io.Writer) error {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(manifest)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Write manifest: %v\n", err)
		return 1
	}
	if err := cost.WriteFile(*siteIndex, cost.RenderViewer); err != nil {
		fmt.Fprintf(os.Stderr, "Write site index: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Indexed %d runs for %s; site lists %d days\n", len(runs), *day, len(manifest.Days))
	return 0
}

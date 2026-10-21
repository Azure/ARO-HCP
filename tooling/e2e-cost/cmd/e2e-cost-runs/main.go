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
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	cost "github.com/Azure/ARO-HCP/tooling/e2e-cost"
)

func main() {
	os.Exit(run())
}

func run() int {
	day := flag.String("day", "", "UTC start day of the runs, YYYY-MM-DD (default: three days ago, once most billing has arrived)")
	timeout := flag.Duration("timeout", 10*time.Minute, "Maximum listing duration")
	flag.Parse()
	if flag.NArg() != 0 || *timeout <= 0 {
		flag.Usage()
		return 2
	}
	selected := time.Now().UTC().AddDate(0, 0, -3)
	if *day != "" {
		parsed, err := time.Parse(time.DateOnly, *day)
		if err != nil {
			flag.Usage()
			return 2
		}
		selected = parsed
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	runs, err := cost.ListRuns(ctx, &http.Client{Timeout: 2 * time.Minute}, selected)
	if err != nil {
		fmt.Fprintf(os.Stderr, "List runs: %v\n", err)
		return 1
	}
	jobs := map[string]int{}
	for _, r := range runs {
		fmt.Println(r.URL)
		jobs[r.Job]++
	}
	for job, n := range jobs {
		fmt.Fprintf(os.Stderr, "%4d %s\n", n, job)
	}
	fmt.Fprintf(os.Stderr, "Listed %d e2e runs from %d jobs started %s (UTC)\n", len(runs), len(jobs), selected.Format(time.DateOnly))
	return 0
}

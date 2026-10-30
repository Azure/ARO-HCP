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
	"strings"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	cost "github.com/Azure/ARO-HCP/tooling/e2e-cost"
)

// listFlag accepts a repeatable, comma-separated flag.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ", ") }
func (l *listFlag) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			*l = append(*l, item)
		}
	}
	return nil
}

func main() { os.Exit(run()) }

func run() int {
	var subscriptions listFlag
	flag.Var(&subscriptions, "subscriptions", "Subscription IDs to chart (repeatable or comma-separated; default: every enabled subscription the credential can read)")
	tenantID := flag.String("tenant-id", "", "Only chart discovered subscriptions in this tenant; ignored with --subscriptions")
	output := flag.String("output", "site/costs.json", "Composite cost page data path")
	months := flag.Int("months", 12, "Calendar months, including the current one, of monthly costs to chart and query")
	dailyMonths := flag.Int("daily-months", 3, "Calendar months, including the current one, of daily costs to chart and query")
	budget := flag.Float64("monthly-budget-usd", 400000, "Monthly amortized USD budget drawn as a reference line and used to judge the projection; 0 charts none")
	top := flag.Int("top", 8, "Groups that keep their own stacked series; the rest are combined into Other")
	timeout := flag.Duration("timeout", 30*time.Minute, "Maximum collection duration")
	flag.Parse()
	if flag.NArg() != 0 || *output == "" || *timeout <= 0 || *months < 2 || *months > 24 || *dailyMonths < 1 || *dailyMonths > *months || *top < 1 || *budget < 0 {
		flag.Usage()
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	client := &http.Client{Timeout: 5 * time.Minute}
	credential, credentialErr := azidentity.NewAzureCLICredential(nil)
	if credentialErr != nil {
		// Leaving the previous page in place is safer than publishing zeros.
		fmt.Fprintf(os.Stderr, "Azure credentials: %v\n", credentialErr)
		return 1
	}

	charted, diagnostics := cost.ResolveCostSubscriptions(ctx, client, credential, subscriptions, *tenantID)
	fmt.Fprintf(os.Stderr, "Charting %d subscriptions...\n", len(charted))

	costs := cost.CollectCosts(ctx, client, credential, charted, cost.CostOptions{
		Months:           *months,
		DailyMonths:      *dailyMonths,
		MonthlyBudgetUSD: *budget,
		Top:              *top,
	}, time.Now())
	costs.Diagnostics = append(diagnostics, costs.Diagnostics...)
	if err := cost.WriteCosts(*output, costs); err != nil {
		fmt.Fprintf(os.Stderr, "Write costs: %v\n", err)
		return 1
	}
	overview := costs.Overview
	fmt.Fprintf(os.Stderr, "%s: month to date $%.2f, forecast rest of month $%.2f from %d of %d subscriptions, projected $%.2f against $%.0f (%s)\n",
		costs.Month, overview.MonthToDateUSD, overview.ForecastRemainingUSD, overview.ForecastSubscriptions, len(charted), overview.ProjectedUSD, *budget, overview.Status)
	for _, diagnostic := range costs.Diagnostics {
		if diagnostic.Severity == "error" {
			fmt.Fprintf(os.Stderr, "%s [%s]: %s\n", diagnostic.Code, diagnostic.Scope, diagnostic.Message)
		}
	}
	fmt.Fprintf(os.Stderr, "Wrote %s for %d subscriptions\n", *output, len(charted))
	if costs.HasErrors() {
		return 1
	}
	return 0
}

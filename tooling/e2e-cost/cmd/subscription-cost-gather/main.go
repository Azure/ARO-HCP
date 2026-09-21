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

type stringsFlag []string

func (s *stringsFlag) String() string { return strings.Join(*s, ", ") }
func (s *stringsFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() { os.Exit(run()) }

func run() int {
	var subscriptions, excludes, keeps stringsFlag
	flag.Var(&subscriptions, "subscription", "Subscription UUID to query (repeatable, required)")
	from := flag.String("from", "", "First UTC usage day, YYYY-MM-DD (default: today minus 3 days)")
	to := flag.String("to", "", "Last UTC usage day, inclusive (default: today minus 2 days)")
	flag.Var(&excludes, "exclude-rg-regex", "Additional Go regex for RGs to exclude (repeatable)")
	flag.Var(&keeps, "keep-rg-regex", "Go regex for RGs to retain despite exclusion rules (repeatable; .* retains all)")
	output := flag.String("output", "subscription-data.json", "Render-ready JSON snapshot path")
	timeout := flag.Duration("timeout", 30*time.Minute, "Maximum collection duration")
	flag.Parse()
	if flag.NArg() != 0 || *timeout <= 0 {
		flag.Usage()
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	fmt.Fprintf(os.Stderr, "Collecting subscription costs for %d supplied scopes...\n", len(subscriptions))
	credential, err := azidentity.NewAzureCLICredential(nil)
	// A missing credential becomes a persisted diagnostic, just like invalid inputs.
	var snapshot *cost.Snapshot
	client := &http.Client{Timeout: 5 * time.Minute}
	if err != nil {
		snapshot = cost.CollectSubscriptions(ctx, client, nil, subscriptions, *from, *to, excludes, keeps, time.Now())
		cost.ResolveSubscriptionNames(ctx, client, nil, snapshot)
	} else {
		snapshot = cost.CollectSubscriptions(ctx, client, credential, subscriptions, *from, *to, excludes, keeps, time.Now())
		cost.ResolveSubscriptionNames(ctx, client, credential, snapshot)
	}
	if err := cost.WriteSnapshot(*output, snapshot); err != nil {
		fmt.Fprintf(os.Stderr, "Write snapshot: %v\n", err)
		return 1
	}
	for _, sub := range snapshot.Subscriptions {
		fmt.Fprintf(os.Stderr, "%s [%s]: USD %.4f retained, %.4f excluded, %.4f total\n", sub.ID, sub.BillingStatus, sub.RetainedUSD, sub.ExcludedUSD, sub.TotalUSD)
	}
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.Severity == "error" {
			fmt.Fprintf(os.Stderr, "%s [%s]: %s\n", diagnostic.Code, diagnostic.Scope, diagnostic.Message)
		}
	}
	fmt.Fprintf(os.Stderr, "Wrote %s (%s through %s UTC, inclusive)\n", *output, snapshot.QueryStart, snapshot.QueryEnd)
	if snapshot.HasErrors() {
		return 1
	}
	return 0
}

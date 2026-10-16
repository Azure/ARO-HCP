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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	cost "github.com/Azure/ARO-HCP/tooling/e2e-cost"
)

func main() {
	os.Exit(run())
}

func run() int {
	job := flag.String("job", "", "Public Prow e2e-parallel job URL (required)")
	output := flag.String("output", "run-data.json", "Render-ready JSON snapshot path")
	timeout := flag.Duration("timeout", 30*time.Minute, "Maximum collection duration")
	flag.Parse()
	if *job == "" || flag.NArg() != 0 || *timeout <= 0 {
		flag.Usage()
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Minute}
	fmt.Fprintln(os.Stderr, "Discovering resource ownership from job artifacts...")
	snapshot := cost.Discover(ctx, client, *job, time.Now())
	fmt.Fprintf(os.Stderr, "Discovered %d resource groups; resolving missing infrastructure subscriptions...\n", len(snapshot.Groups))
	credential, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		snapshot.Diagnose("error", "azure-credential", "billing", "Cannot initialize Azure CLI credentials; check az login and AZURE_CONFIG_DIR")
	} else {
		cost.ResolveInfraSubscriptions(ctx, client, credential, snapshot)
		fmt.Fprintln(os.Stderr, "Collecting Azure amortized costs...")
		cost.Collect(ctx, client, credential, snapshot)
	}
	if err := cost.WriteSnapshot(*output, snapshot); err != nil {
		fmt.Fprintf(os.Stderr, "Write snapshot: %v\n", err)
		return 1
	}
	for _, d := range snapshot.Diagnostics {
		if d.Severity == "error" {
			fmt.Fprintf(os.Stderr, "%s [%s]: %s\n", d.Code, d.Scope, d.Message)
		}
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", *output)
	if snapshot.HasErrors() {
		return 1
	}
	return 0
}

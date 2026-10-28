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

package shared

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/cmd/workflow"
)

// Name selects this workflow with --workflow and prefixes its options.
const Name = "shared-leftovers"

// Workflow is the shared-leftovers cleanup workflow as seen by the command.
type Workflow struct {
	subscriptionID string
	wait           bool
}

var _ workflow.Workflow = (*Workflow)(nil)

// New returns a shared-leftovers workflow with default options.
func New() *Workflow {
	return &Workflow{wait: true}
}

func (w *Workflow) Name() string { return Name }

func (w *Workflow) AttachOptions(flags *pflag.FlagSet) {
	flags.StringVar(&w.subscriptionID, "subscription-id", w.subscriptionID, "Subscription ID to clean (required).")
	flags.BoolVar(&w.wait, "wait", w.wait, "Wait for long-running deletions to complete.")
}

func (w *Workflow) Validate(workflow.Common) error {
	if strings.TrimSpace(w.subscriptionID) == "" {
		return fmt.Errorf("%s.subscription-id is required", Name)
	}
	return nil
}

func (w *Workflow) LogValues() []any {
	return []any{"subscriptionID", strings.TrimSpace(w.subscriptionID)}
}

func (w *Workflow) Run(ctx context.Context, common workflow.Common, credential azcore.TokenCredential) error {
	// Only this workflow performs Microsoft Graph directory reads, so the
	// (optionally dedicated) Graph credential is resolved here. This keeps a
	// partial GRAPH_AZURE_* configuration from failing workflows that never
	// touch Graph.
	graphCredential, err := newGraphCredential(credential)
	if err != nil {
		return err
	}
	return Run(ctx, RunOptions{
		SubscriptionID:  strings.TrimSpace(w.subscriptionID),
		AzureCredential: credential,
		GraphCredential: graphCredential,
		DryRun:          common.DryRun,
		Wait:            w.wait,
		Parallelism:     common.Parallelism,
	})
}

// newGraphCredential returns the credential used for Microsoft Graph directory
// reads. When the GRAPH_AZURE_TENANT_ID, GRAPH_AZURE_CLIENT_ID and
// GRAPH_AZURE_CLIENT_SECRET environment variables are all set, a dedicated
// client-secret credential is built for that identity (allowing directory read
// permissions to be supplied by a different service principal than the one used
// for ARM). Otherwise it falls back to the provided ARM credential.
func newGraphCredential(fallback azcore.TokenCredential) (azcore.TokenCredential, error) {
	tenantID := strings.TrimSpace(os.Getenv("GRAPH_AZURE_TENANT_ID"))
	clientID := strings.TrimSpace(os.Getenv("GRAPH_AZURE_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GRAPH_AZURE_CLIENT_SECRET"))

	if tenantID == "" && clientID == "" && clientSecret == "" {
		return fallback, nil
	}
	if tenantID == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("GRAPH_AZURE_TENANT_ID, GRAPH_AZURE_CLIENT_ID and GRAPH_AZURE_CLIENT_SECRET must all be set to use a dedicated Graph credential")
	}

	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create dedicated Graph credential: %w", err)
	}
	return cred, nil
}

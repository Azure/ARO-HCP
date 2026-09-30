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

package engine

import (
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorepolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	certsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/certificates"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

const (
	// CertificateVaultURL is the only vault the certificate workflow ever touches.
	CertificateVaultURL              = "https://aro-hcp-dev-svc-kv.vault.azure.net"
	globalInfrastructureSubscription = "1d3378d3-5a3f-4712-85a1-2485495dfc4b"
	ci01InfrastructureSubscription   = "0ef1ad54-9296-44cd-9600-5dc8e9a74034"
)

// CertificateWorkflowOptions controls the certificate cleanup workflow.
type CertificateWorkflowOptions struct {
	DryRun       bool
	DeleteActive bool
	PurgeDeleted bool
	MaxDeletions int
	MaxPurges    int
	Parallelism  int
	Policy       policy.CertificateDiscoveryPolicy
}

// CertificateCleanupWorkflow builds the cleanup workflow for CI certificates in
// the shared dev service vault. The workflow soft-deletes (and immediately
// purges) eligible active certificates first, then purges eligible deleted
// certificates, including any tombstone the first step could not purge.
// Resource-group ownership is read from both fixed dev infrastructure
// subscriptions whenever a policy rule needs an owner guard.
func CertificateCleanupWorkflow(credential azcore.TokenCredential, opts CertificateWorkflowOptions) (*runner.Engine, error) {
	if credential == nil {
		return nil, fmt.Errorf("azure credential is required")
	}
	// Do not replay a DELETE after a retry delay without rerunning the safety checks.
	client, err := azcertificates.NewClient(CertificateVaultURL, credential, &azcertificates.ClientOptions{
		ClientOptions: azcore.ClientOptions{Retry: azcorepolicy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create certificates client: %w", err)
	}

	subscriptions := []string{globalInfrastructureSubscription, ci01InfrastructureSubscription}
	groups := make(map[string]certsteps.ResourceGroupLister, len(subscriptions))
	for _, subscription := range subscriptions {
		groupsClient, err := armresources.NewResourceGroupsClient(subscription, credential, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource groups client for %s: %w", subscription, err)
		}
		groups[subscription] = groupsClient
	}

	session, err := certsteps.NewSession(certsteps.Config{
		Client:       client,
		VaultURL:     CertificateVaultURL,
		Policy:       opts.Policy,
		Owners:       certsteps.NewOwnerInventory(subscriptions, groups, time.Now),
		DryRun:       opts.DryRun,
		DeleteActive: opts.DeleteActive,
		PurgeDeleted: opts.PurgeDeleted,
		MaxDeletions: opts.MaxDeletions,
		MaxPurges:    opts.MaxPurges,
	})
	if err != nil {
		return nil, err
	}
	return &runner.Engine{
		Steps:       session.Steps(),
		Parallelism: opts.Parallelism,
		DryRun:      opts.DryRun,
		Wait:        true,
		PostRunFn:   session.Finish,
	}, nil
}

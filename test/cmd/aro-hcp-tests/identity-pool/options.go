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

package identitypool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/e2e"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

const (
	deploymentStackName       = "aro-hcp-msi-pool"
	deploymentStackRP         = "Microsoft.Resources"
	deploymentStackType       = "deploymentStacks"
	deploymentStackAPIVersion = "2024-03-01"
)

func DefaultApplyOptions() *RawApplyOptions {
	return &RawApplyOptions{
		PoolSize: -1,
	}
}

func BindApplyOptions(opts *RawApplyOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Environment, "environment", opts.Environment, "Environment short name. One of: int, stg, dev, prod")
	cmd.Flags().IntVar(&opts.PoolSize, "pool-size", opts.PoolSize, "Number of resource groups to create in the identity pool")
	if err := cmd.MarkFlagRequired("environment"); err != nil {
		return fmt.Errorf("failed to mark flag %q as required: %w", "environment", err)
	}
	return nil
}

type RawApplyOptions struct {
	Environment string
	PoolSize    int
}

type validatedApplyOptions struct {
	*RawApplyOptions
}

type ValidatedApplyOptions struct {
	*validatedApplyOptions
}

type completedApplyOptions struct {
	Template        map[string]interface{}
	IdentityPool    identityPool
	SubscriptionID  string
	AzureCredential azcore.TokenCredential
}

type ApplyOptions struct {
	*completedApplyOptions
}

func (o *RawApplyOptions) Validate() (*ValidatedApplyOptions, error) {
	switch o.Environment {
	case "dev", "int", "stg", "prod":
	default:
		return nil, fmt.Errorf("invalid environment %q: must be 'dev', 'int', 'stg', or 'prod'", o.Environment)
	}

	if o.PoolSize != -1 && o.PoolSize <= 0 {
		return nil, fmt.Errorf("--pool-size must be a positive integer")
	}

	return &ValidatedApplyOptions{
		validatedApplyOptions: &validatedApplyOptions{
			RawApplyOptions: o,
		},
	}, nil
}

func (o *ValidatedApplyOptions) Complete(ctx context.Context) (*ApplyOptions, error) {

	pool := identityPoolMapping[o.Environment]
	if o.PoolSize != -1 {
		pool.Size = o.PoolSize
	}

	tc := framework.NewTestContext()
	cred, err := tc.AzureCredential()
	if err != nil {
		return nil, fmt.Errorf("failed getting Azure credential: %w", err)
	}

	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed getting subscription ID: %w", err)
	}
	if err := validateSubscriptionIDHash(pool, subscriptionID); err != nil {
		return nil, fmt.Errorf("failed validating subscription ID hash: %w", err)
	}

	// Deployment Stacks require an ARM JSON template. The test suite embeds pre-generated
	// templates under test/e2e/test-artifacts/generated-test-artifacts.
	template, err := e2e.TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/msi-pools.json")
	if err != nil {
		return nil, fmt.Errorf("failed reading template file: %w", err)
	}
	bicepTemplateMap := map[string]interface{}{}
	if err := json.Unmarshal(template, &bicepTemplateMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Bicep template JSON: %w", err)
	}

	return &ApplyOptions{
		completedApplyOptions: &completedApplyOptions{
			Template:        bicepTemplateMap,
			IdentityPool:    pool,
			SubscriptionID:  subscriptionID,
			AzureCredential: cred,
		},
	}, nil
}

func (o *ApplyOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	parameters := map[string]any{
		"poolSize": map[string]any{
			"value": o.IdentityPool.Size,
		},
		"resourceGroupBaseName": map[string]any{
			"value": o.IdentityPool.ResourceGroupBaseName,
		},
	}

	resourceID := fmt.Sprintf(
		"/subscriptions/%s/providers/%s/%s/%s",
		o.SubscriptionID,
		deploymentStackRP,
		deploymentStackType,
		deploymentStackName,
	)

	properties := map[string]any{
		"template":   o.Template,
		"parameters": parameters,
		"denySettings": map[string]any{
			"mode": "none",
		},
		"actionOnUnmanage": map[string]any{
			"resources":        "delete",
			"resourceGroups":   "delete",
			"managementGroups": "delete",
		},
	}

	logger.Info(
		"applying deployment stack",
		"subscriptionID", o.SubscriptionID,
		"stackName", deploymentStackName,
		"location", o.IdentityPool.Location,
		"poolSize", o.IdentityPool.Size,
	)

	clientFactory, err := armresources.NewClientFactory(o.SubscriptionID, o.AzureCredential, nil)
	if err != nil {
		return fmt.Errorf("failed creating ARM resources client factory: %w", err)
	}

	poller, err := clientFactory.NewClient().BeginCreateOrUpdateByID(
		ctx,
		resourceID,
		deploymentStackAPIVersion,
		armresources.GenericResource{
			Location:   to.Ptr(o.IdentityPool.Location),
			Properties: properties,
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed starting deployment stack apply: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, &azruntime.PollUntilDoneOptions{Frequency: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("failed waiting for deployment stack apply to complete: %w", err)
	}

	return nil
}

func validateSubscriptionIDHash(pool identityPool, subscriptionID string) error {
	actual := subscriptionIDHash(subscriptionID)
	if !strings.HasPrefix(actual, pool.SubscriptionIDHash) {
		return fmt.Errorf("wrong subscriptionID: expected subscriptionIDHash %q, got %q", pool.SubscriptionIDHash, actual)
	}
	return nil
}

func subscriptionIDHash(subscriptionID string) string {
	normalized := strings.ToLower(strings.TrimSpace(subscriptionID))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

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

package slots

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

// VerifyCustomerSubscriptionName checks that slotSubscriptionName matches
// exactly one customer-*-subscription-name file across the supplied cluster
// profile dirs. It returns the validated name (not a subscription ID) and the
// single cluster profile dir that matched. Downstream E2E steps use the
// returned dir to load the tenant/service-principal credentials that own the
// leased subscription, which is what allows a single job to lease slots that
// live in more than one Azure tenant.
func VerifyCustomerSubscriptionName(clusterProfileDirs []string, slotSubscriptionName string) (string, string, error) {
	if len(clusterProfileDirs) == 0 {
		return "", "", errors.New("cluster profile dirs are empty")
	}

	if slotSubscriptionName == "" {
		return "", "", errors.New("slot subscription name is empty")
	}

	var matchedFile string
	var matchedDir string
	for _, clusterProfileDir := range clusterProfileDirs {
		if clusterProfileDir == "" {
			return "", "", errors.New("cluster profile dir is empty")
		}

		entries, err := os.ReadDir(clusterProfileDir)
		if err != nil {
			return "", "", fmt.Errorf("failed to read cluster profile dir %q: %w", clusterProfileDir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !isCustomerSubscriptionNameFile(entry.Name()) {
				continue
			}

			candidatePath := filepath.Join(clusterProfileDir, entry.Name())
			data, err := os.ReadFile(candidatePath)
			if err != nil {
				return "", "", fmt.Errorf("failed to read customer subscription name %q: %w", candidatePath, err)
			}

			if strings.TrimSpace(string(data)) != slotSubscriptionName {
				continue
			}

			if matchedFile != "" {
				return "", "", fmt.Errorf(
					"multiple customer subscription name files matched slot subscription %q: %s, %s",
					slotSubscriptionName,
					matchedFile,
					candidatePath,
				)
			}

			matchedFile = candidatePath
			matchedDir = clusterProfileDir
		}
	}

	if matchedFile == "" {
		return "", "", fmt.Errorf("no customer subscription name file matched slot subscription %q in %s", slotSubscriptionName, strings.Join(clusterProfileDirs, ", "))
	}

	return slotSubscriptionName, matchedDir, nil
}

func isCustomerSubscriptionNameFile(name string) bool {
	return strings.HasPrefix(name, "customer-") && strings.HasSuffix(name, "-subscription-name")
}

// ResolvePoolSubscriptions resolves only demanded subscriptions. An empty
// infrastructureSubscriptionName selects E2E-only resolution without reading
// the deployment environment's infrastructure profile binding.
func ResolvePoolSubscriptions(
	ctx context.Context,
	clusterProfileDir string,
	deployEnvironment string,
	e2eSubscriptionName string,
	infrastructureSubscriptionName string,
) (ResolvedSubscriptions, error) {
	return resolvePoolSubscriptions(ctx, clusterProfileDir, deployEnvironment, e2eSubscriptionName, infrastructureSubscriptionName, nil, nil)
}

func resolvePoolSubscriptions(
	ctx context.Context,
	clusterProfileDir, deployEnvironment, e2eSubscriptionName, infrastructureSubscriptionName string,
	credentialOptions *azidentity.ClientSecretCredentialOptions,
	clientOptions *azcorearm.ClientOptions,
) (ResolvedSubscriptions, error) {
	if err := validateDeploymentEnvironmentName(deployEnvironment); err != nil {
		return ResolvedSubscriptions{}, err
	}
	if strings.TrimSpace(e2eSubscriptionName) == "" {
		return ResolvedSubscriptions{}, errors.New("E2E subscription name is empty")
	}
	credential, err := NewClusterProfileCredential(clusterProfileDir, credentialOptions)
	if err != nil {
		return ResolvedSubscriptions{}, err
	}
	clientFactory, err := armsubscriptions.NewClientFactory(credential, clientOptions)
	if err != nil {
		return ResolvedSubscriptions{}, fmt.Errorf("failed creating subscriptions client factory: %w", err)
	}

	subscriptionIDs := map[string]string{}
	pager := clientFactory.NewClient().NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return ResolvedSubscriptions{}, fmt.Errorf("failed listing subscriptions: %w", err)
		}
		selected := make([]*armsubscriptions.Subscription, 0, len(page.Value))
		for _, subscription := range page.Value {
			if subscription == nil || subscription.DisplayName == nil {
				continue
			}
			name := strings.TrimSpace(*subscription.DisplayName)
			if name == e2eSubscriptionName || (infrastructureSubscriptionName != "" && name == infrastructureSubscriptionName) {
				selected = append(selected, subscription)
			}
		}
		if err := addSubscriptionIDs(subscriptionIDs, selected); err != nil {
			return ResolvedSubscriptions{}, err
		}
	}

	e2eSubscriptionID := strings.TrimSpace(subscriptionIDs[e2eSubscriptionName])
	if e2eSubscriptionID == "" {
		return ResolvedSubscriptions{}, fmt.Errorf("subscription with name %q was not visible to cluster profile %q", e2eSubscriptionName, clusterProfileDir)
	}
	resolved := ResolvedSubscriptions{
		E2E: ResolvedSubscription{Name: e2eSubscriptionName, ID: e2eSubscriptionID},
	}
	if infrastructureSubscriptionName == "" {
		return resolved, nil
	}
	infrastructureSubscriptionID := strings.TrimSpace(subscriptionIDs[infrastructureSubscriptionName])
	if infrastructureSubscriptionID == "" {
		return ResolvedSubscriptions{}, fmt.Errorf("subscription with name %q was not visible to cluster profile %q", infrastructureSubscriptionName, clusterProfileDir)
	}

	boundInfrastructureSubscriptionID, err := ReadRequiredProfileFile(clusterProfileDir, fmt.Sprintf("infra-%s-subscription-id", deployEnvironment))
	if err != nil {
		return ResolvedSubscriptions{}, err
	}
	if !strings.EqualFold(boundInfrastructureSubscriptionID, infrastructureSubscriptionID) {
		return ResolvedSubscriptions{}, fmt.Errorf(
			"pool infrastructure subscription %q resolved to %q, but cluster profile %q binds deploy environment %q to %q",
			infrastructureSubscriptionName,
			infrastructureSubscriptionID,
			clusterProfileDir,
			deployEnvironment,
			boundInfrastructureSubscriptionID,
		)
	}

	resolved.Infrastructure = ResolvedSubscription{
		Name: infrastructureSubscriptionName,
		ID:   infrastructureSubscriptionID,
	}
	return resolved, nil
}

func addSubscriptionIDs(ids map[string]string, subscriptions []*armsubscriptions.Subscription) error {
	for _, subscription := range subscriptions {
		if subscription == nil || subscription.DisplayName == nil || subscription.SubscriptionID == nil {
			return errors.New("subscription list returned an entry without display name or ID")
		}
		name := strings.TrimSpace(*subscription.DisplayName)
		id := strings.TrimSpace(*subscription.SubscriptionID)
		if name == "" || id == "" {
			return errors.New("subscription list returned an entry with blank display name or ID")
		}
		if existing, found := ids[name]; found && !strings.EqualFold(existing, id) {
			return fmt.Errorf("subscription display name %q is ambiguous: IDs %q and %q", name, existing, id)
		}
		ids[name] = id
	}
	return nil
}

func NewClusterProfileCredential(clusterProfileDir string, options *azidentity.ClientSecretCredentialOptions) (*azidentity.ClientSecretCredential, error) {
	tenantID, err := ReadRequiredProfileFile(clusterProfileDir, "tenant")
	if err != nil {
		return nil, err
	}
	clientID, err := ReadRequiredProfileFile(clusterProfileDir, "client-id")
	if err != nil {
		return nil, err
	}
	clientSecret, err := ReadRequiredProfileFile(clusterProfileDir, "client-secret")
	if err != nil {
		return nil, err
	}
	credential, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, options)
	if err != nil {
		return nil, fmt.Errorf("failed creating Azure credential from cluster profile %q: %w", clusterProfileDir, err)
	}
	return credential, nil
}

func ReadRequiredProfileFile(clusterProfileDir, fileName string) (string, error) {
	if strings.TrimSpace(clusterProfileDir) == "" {
		return "", errors.New("cluster profile dir is empty")
	}
	path := filepath.Join(clusterProfileDir, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read cluster profile file %q: %w", path, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("cluster profile file %q is empty", path)
	}
	return value, nil
}

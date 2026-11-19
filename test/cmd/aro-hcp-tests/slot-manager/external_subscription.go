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

package slotmanager

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/e2eidentities"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/mockidentities"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/filelock"
)

const externalSubscriptionTenant = "64dc69e4-d083-49fc-9569-ebece1dd1408"

type externalE2EHandler interface {
	PreflightExternalSubscription(context.Context, []assets.PoolRequest, string, azcore.TokenCredential) error
	ReconcileExternalSubscription(context.Context, []assets.PoolRequest, string, azcore.TokenCredential, bool) error
}

type externalMockHandler interface {
	PreflightExternalSubscription(context.Context, azcore.TokenCredential, string, []slots.ExpandedSlot, bool) error
	CheckExternalSubscription(context.Context, azcore.TokenCredential, string, []slots.ExpandedSlot) error
	ApplyExternalSubscription(context.Context, azcore.TokenCredential, string, []slots.ExpandedSlot, bool) error
}

type RawReconcileExternalSubscriptionOptions struct {
	SubscriptionID    string
	RepoRoot          string
	ConfigFile        string
	SlotCatalog       string
	Apply             bool
	PrepareIdentities bool
	Out               io.Writer
}

type validatedReconcileExternalSubscriptionOptions struct {
	*RawReconcileExternalSubscriptionOptions
}

type ValidatedReconcileExternalSubscriptionOptions struct {
	*validatedReconcileExternalSubscriptionOptions
}

type completedReconcileExternalSubscriptionOptions struct {
	SubscriptionID    string
	RepoRoot          string
	Apply             bool
	PrepareIdentities bool
	Out               io.Writer
	clientOptions     *arm.ClientOptions
	subscriptions     *armsubscriptions.Client
	providers         *armresources.ProvidersClient
	deployments       *armdeployments.DeploymentsClient
	graph             *msgraph.GraphServiceClient
	catalog           *slots.Catalog
	config            externalSubscriptionConfig
	e2e               externalE2EHandler
	mock              externalMockHandler
}

type ReconcileExternalSubscriptionOptions struct {
	*completedReconcileExternalSubscriptionOptions
}

type externalSubscriptionConfig struct {
	Clouds struct {
		Dev struct {
			Defaults struct {
				OwnerIDs      string `yaml:"entraAppOwnerIds"`
				OwnersGroupID string `yaml:"entraAppOwnersGroupId"`
				CI            struct {
					Providers string `yaml:"e2eSubscriptionProviders"`
					Dev       struct {
						Bot struct {
							ApplicationName string `yaml:"applicationName"`
						} `yaml:"bot"`
						MockIdentities struct {
							FirstParty externalApplicationConfig `yaml:"firstParty"`
							ARMHelper  externalApplicationConfig `yaml:"armHelper"`
							MSIMock    externalApplicationConfig `yaml:"msiMock"`
						} `yaml:"mockIdentities"`
					} `yaml:"dev"`
				} `yaml:"ci"`
			} `yaml:"defaults"`
		} `yaml:"dev"`
	} `yaml:"clouds"`
}

type externalApplicationConfig struct {
	ApplicationName string `yaml:"applicationName"`
}

func DefaultReconcileExternalSubscriptionOptions() *RawReconcileExternalSubscriptionOptions {
	return &RawReconcileExternalSubscriptionOptions{}
}

func BindReconcileExternalSubscriptionOptions(options *RawReconcileExternalSubscriptionOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.SubscriptionID, "subscription-id", options.SubscriptionID, "Immutable UUID of the external Red Hat DEV E2E subscription.")
	command.Flags().StringVar(&options.RepoRoot, "repo-root", options.RepoRoot, "Source checkout root containing config and catalog, also used to report revision (required).")
	command.Flags().StringVar(&options.ConfigFile, "config-file", options.ConfigFile, "DEV CI configuration; defaults to config/config-dev-ci.yaml below repo-root.")
	command.Flags().StringVar(&options.SlotCatalog, "slot-catalog", options.SlotCatalog, "Slot catalog; defaults to test/e2e-config/e2e-slots.yaml below repo-root.")
	command.Flags().BoolVar(&options.Apply, "apply", options.Apply, "Apply additive repairs; without this flag all Azure operations are read-only.")
	command.Flags().BoolVar(&options.PrepareIdentities, "prepare-identities", options.PrepareIdentities, "Explicitly prepare missing slot identities and credentials; requires --apply and privileged Graph/Key Vault access.")
	return nil
}

func newReconcileExternalSubscriptionCommand() (*cobra.Command, error) {
	options := DefaultReconcileExternalSubscriptionOptions()
	command := &cobra.Command{
		Use:   "reconcile-external-subscription",
		Short: "Check or non-destructively repair one external DEV E2E subscription.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.Out = cmd.OutOrStdout()
			return ReconcileExternalSubscription(cmd.Context(), options)
		},
	}
	if err := BindReconcileExternalSubscriptionOptions(options, command); err != nil {
		return nil, err
	}
	return command, nil
}

func ReconcileExternalSubscription(ctx context.Context, options *RawReconcileExternalSubscriptionOptions) error {
	validated, err := options.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}
	return completed.Run(ctx)
}

func (o *RawReconcileExternalSubscriptionOptions) Validate() (*ValidatedReconcileExternalSubscriptionOptions, error) {
	parsed, err := uuid.Parse(o.SubscriptionID)
	if err != nil || parsed == uuid.Nil || !strings.EqualFold(parsed.String(), o.SubscriptionID) {
		return nil, fmt.Errorf("--subscription-id must be an explicit nonzero canonical UUID, not a subscription name")
	}
	if strings.TrimSpace(o.RepoRoot) == "" {
		return nil, fmt.Errorf("--repo-root is required; use make -C test reconcile-external-subscription SUBSCRIPTION_ID=%s", o.SubscriptionID)
	}
	if o.PrepareIdentities && !o.Apply {
		return nil, fmt.Errorf("--prepare-identities requires --apply; check mode never prepares identities")
	}
	return &ValidatedReconcileExternalSubscriptionOptions{
		validatedReconcileExternalSubscriptionOptions: &validatedReconcileExternalSubscriptionOptions{RawReconcileExternalSubscriptionOptions: o},
	}, nil
}

func (o *ValidatedReconcileExternalSubscriptionOptions) Complete(_ context.Context) (*ReconcileExternalSubscriptionOptions, error) {
	root, err := filepath.Abs(o.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve source checkout: %w", err)
	}
	configFile := o.ConfigFile
	if configFile == "" {
		configFile = filepath.Join(root, "config/config-dev-ci.yaml")
	}
	slotCatalog := o.SlotCatalog
	if slotCatalog == "" {
		slotCatalog = filepath.Join(root, slots.DefaultCatalogRelPath)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("read DEV CI configuration: %w", err)
	}
	var config externalSubscriptionConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode DEV CI configuration: %w", err)
	}
	ci := config.Clouds.Dev.Defaults.CI
	if ci.Providers == "" || ci.Dev.Bot.ApplicationName == "" || ci.Dev.MockIdentities.FirstParty.ApplicationName == "" || ci.Dev.MockIdentities.ARMHelper.ApplicationName == "" || ci.Dev.MockIdentities.MSIMock.ApplicationName == "" {
		return nil, fmt.Errorf("configuration must declare DEV providers, bot and all three shared mock applications")
	}
	catalog, err := slots.LoadCatalog(slotCatalog)
	if err != nil {
		return nil, err
	}
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	return &ReconcileExternalSubscriptionOptions{
		completedReconcileExternalSubscriptionOptions: &completedReconcileExternalSubscriptionOptions{
			SubscriptionID:    strings.ToLower(o.SubscriptionID),
			RepoRoot:          root,
			Apply:             o.Apply,
			PrepareIdentities: o.PrepareIdentities,
			Out:               out,
			catalog:           catalog,
			config:            config,
		},
	}, nil
}

type externalAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
	State    string `json:"state"`
}

type externalSelection struct {
	requests             []assets.PoolRequest
	slots                []slots.ExpandedSlot
	poolNames            []string
	usesSubscriptionName bool
}

func selectExternalSubscription(catalog *slots.Catalog, account externalAccount) (externalSelection, error) {
	result := externalSelection{}
	if account.TenantID != externalSubscriptionTenant {
		return result, fmt.Errorf("subscription tenant %q is unsupported; only Red Hat DEV tenant %s is permitted", account.TenantID, externalSubscriptionTenant)
	}
	if account.State != "Enabled" {
		return result, fmt.Errorf("subscription %s is not Enabled (state %q)", account.ID, account.State)
	}
	for _, environment := range catalog.EnvironmentNames() {
		request := assets.PoolRequest{Environment: environment, IncludeUnmanaged: true}
		for _, pool := range catalog.Environments[environment].Pools {
			if !strings.EqualFold(pool.Subscriptions.E2E, account.ID) && pool.Subscriptions.E2E != account.Name {
				continue
			}
			if environment != "dev" && environment != "dev-xs" {
				return result, fmt.Errorf("matching pool %s/%s is unsupported: only dev and dev-xs are permitted", environment, pool.Name)
			}
			asset := pool.SlotAssets.E2EIdentities
			if asset == nil || asset.Provisioning != slots.IdentityProvisioningUnmanaged {
				return result, fmt.Errorf("pool %s/%s is not an unmanaged external E2E identity pool", environment, pool.Name)
			}
			if pool.SlotAssets.MockIdentities == nil {
				return result, fmt.Errorf("pool %s/%s has no slot mock identities", environment, pool.Name)
			}
			if !strings.EqualFold(pool.Subscriptions.E2E, account.ID) {
				result.usesSubscriptionName = true
			}
			request.Pools = append(request.Pools, pool)
			result.poolNames = append(result.poolNames, environment+"/"+pool.Name)
			for _, slot := range slots.ExpandSlotsForPool(environment, pool) {
				slot.Subscriptions.E2E.ID = account.ID
				result.slots = append(result.slots, slot)
			}
		}
		if len(request.Pools) > 0 {
			result.requests = append(result.requests, request)
		}
	}
	if len(result.requests) == 0 {
		return result, fmt.Errorf("no unmanaged DEV/dev-xs pools match subscription %s (%s); add the target to the catalog before maintenance", account.ID, account.Name)
	}
	return result, nil
}

func (o *ReconcileExternalSubscriptionOptions) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	lock, err := os.OpenFile(filepath.Join(os.TempDir(), "aro-hcp-external-subscription-"+o.SubscriptionID+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open subscription maintenance lock: %w", err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock.Fd()); err != nil {
		return fmt.Errorf("lock subscription maintenance: %w", err)
	}
	defer filelock.Unlock(lock.Fd())

	credential, err := framework.NewAzureCredential(true, o.SubscriptionID)
	if err != nil {
		return fmt.Errorf("create Azure credential: %w", err)
	}
	if err := o.newClients(credential); err != nil {
		return err
	}
	selection, account, err := o.selectSubscription(ctx)
	if err != nil {
		return err
	}
	revision, err := exec.CommandContext(ctx, "git", "-C", o.RepoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("report source revision: %w", err)
	}
	fmt.Fprintf(o.Out, "revision=%s subscription=%s tenant=%s mode=%s pools=%s slots=%d\n", strings.TrimSpace(string(revision)), o.SubscriptionID, account.TenantID, map[bool]string{false: "check", true: "apply"}[o.Apply], strings.Join(selection.poolNames, ","), len(selection.slots))
	inventories, err := o.catalog.AssetInventories()
	if err != nil {
		return err
	}
	for i := range selection.requests {
		selection.requests[i].Out = o.Out
		selection.requests[i].AssetInventories = inventories
	}
	return o.reconcile(ctx, credential, selection)
}

func (o *ReconcileExternalSubscriptionOptions) reconcile(ctx context.Context, credential azcore.TokenCredential, selection externalSelection) error {
	if o.e2e == nil {
		o.e2e = e2eidentities.NewHandler()
	}
	if o.mock == nil {
		o.mock = mockidentities.NewHandler()
	}
	e2eHandler, mockHandler := o.e2e, o.mock
	// Resolve every shared prerequisite and preview both deployments before the first write.
	deployments, err := o.sharedDeployments(ctx)
	if err != nil {
		return fmt.Errorf("shared identity preflight (prepare shared DEV applications with the existing privileged DEV identity workflow): %w", err)
	}
	if err := e2eHandler.PreflightExternalSubscription(ctx, selection.requests, o.SubscriptionID, credential); err != nil {
		return fmt.Errorf("E2E container safety preflight: %w", err)
	}
	if o.PrepareIdentities {
		// The explicit privileged path uses the same authoritative owners as the existing asset workflow.
		if err := requireExternalOwnerSettings(o.config.Clouds.Dev.Defaults.OwnerIDs, o.config.Clouds.Dev.Defaults.OwnersGroupID); err != nil {
			return err
		}
	}
	if o.Apply {
		if err := mockHandler.PreflightExternalSubscription(ctx, credential, o.SubscriptionID, selection.slots, o.PrepareIdentities); err != nil {
			return fmt.Errorf("slot identity preflight: %w", err)
		}
	}
	for _, deployment := range deployments {
		if err := o.previewDeployment(ctx, deployment, false); err != nil {
			return fmt.Errorf("RBAC safety preflight: %w", err)
		}
	}
	providers := externalProviders(o.config.Clouds.Dev.Defaults.CI.Providers)
	missingProviders, err := o.missingProviders(ctx, providers)
	if err != nil {
		return fmt.Errorf("provider preflight: %w", err)
	}
	if !o.Apply {
		var blocked []string
		if len(missingProviders) > 0 {
			blocked = append(blocked, "providers not Registered: "+strings.Join(missingProviders, ", "))
		}
		for _, deployment := range deployments {
			if err := o.previewDeployment(ctx, deployment, true); err != nil {
				blocked = append(blocked, err.Error())
			}
		}
		if err := e2eHandler.ReconcileExternalSubscription(ctx, selection.requests, o.SubscriptionID, credential, false); err != nil {
			blocked = append(blocked, "E2E containers: "+err.Error())
		}
		if err := mockHandler.CheckExternalSubscription(ctx, credential, o.SubscriptionID, selection.slots); err != nil {
			blocked = append(blocked, "slot identities: "+err.Error())
		}
		if len(blocked) > 0 {
			return fmt.Errorf("subscription is not ready; rerun with --apply for permitted repairs:\n%s", strings.Join(blocked, "\n"))
		}
		fmt.Fprintln(o.Out, "ready: providers, shared RBAC, E2E containers and slot mock grants")
		return nil
	}
	for _, provider := range missingProviders {
		if _, err := o.providers.Register(ctx, provider, nil); err != nil {
			return fmt.Errorf("register provider %s: %w", provider, err)
		}
	}
	if len(missingProviders) > 0 {
		var providerReadiness error
		if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
			providerReadiness = o.checkProviders(ctx, providers)
			return providerReadiness == nil, nil
		}); err != nil {
			return fmt.Errorf("provider readiness timed out: %v: %w", providerReadiness, err)
		}
	}
	for _, deployment := range deployments {
		if err := o.applyDeployment(ctx, deployment); err != nil {
			return fmt.Errorf("incremental RBAC deployment %s: %w", deployment.name, err)
		}
	}
	if err := e2eHandler.ReconcileExternalSubscription(ctx, selection.requests, o.SubscriptionID, credential, true); err != nil {
		return fmt.Errorf("reconcile E2E containers: %w", err)
	}
	if err := mockHandler.ApplyExternalSubscription(ctx, credential, o.SubscriptionID, selection.slots, o.PrepareIdentities); err != nil {
		return fmt.Errorf("reconcile slot identities: %w", err)
	}
	var readiness error
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		for _, deployment := range deployments {
			if readiness = o.previewDeployment(ctx, deployment, true); readiness != nil {
				return false, nil
			}
		}
		if readiness = e2eHandler.ReconcileExternalSubscription(ctx, selection.requests, o.SubscriptionID, credential, false); readiness != nil {
			return false, nil
		}
		readiness = mockHandler.CheckExternalSubscription(ctx, credential, o.SubscriptionID, selection.slots)
		return readiness == nil, nil
	})
	if err != nil {
		return fmt.Errorf("readiness remains blocked after bounded propagation wait: %v: %w", readiness, err)
	}
	fmt.Fprintln(o.Out, "ready: providers, shared RBAC, E2E containers and slot mock grants; no infrastructure assets or permissions changed")
	return nil
}

func requireExternalOwnerSettings(ids, group string) error {
	for name, value := range map[string]string{mockidentities.OwnerIDsEnv: ids, mockidentities.OwnersGroupIDEnv: group} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("identity preparation requires authoritative %s in DEV CI configuration", name)
		}
		if existing := os.Getenv(name); existing != "" && existing != value {
			return fmt.Errorf("%s differs from DEV CI configuration; refusing ambiguous privileged owner settings", name)
		}
	}
	// Existing identity preparation reads these process-wide settings. This command is a single CLI operation.
	if err := os.Setenv(mockidentities.OwnerIDsEnv, ids); err != nil {
		return err
	}
	return os.Setenv(mockidentities.OwnersGroupIDEnv, group)
}

func externalProviders(configured string) []string {
	providers := []string{"Microsoft.Authorization"}
	for _, provider := range strings.Split(configured, ",") {
		provider = strings.TrimSpace(provider)
		if provider != "" && !slices.Contains(providers, provider) {
			providers = append(providers, provider)
		}
	}
	return providers
}

func (o *ReconcileExternalSubscriptionOptions) checkProviders(ctx context.Context, providers []string) error {
	missing, err := o.missingProviders(ctx, providers)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("providers not Registered: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (o *ReconcileExternalSubscriptionOptions) missingProviders(ctx context.Context, providers []string) ([]string, error) {
	registered := make(map[string]bool)
	pager := o.providers.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("read provider registrations: %w", err)
		}
		for _, provider := range page.Value {
			if provider != nil && ptr.Deref(provider.RegistrationState, "") == "Registered" {
				registered[strings.ToLower(ptr.Deref(provider.Namespace, ""))] = true
			}
		}
	}
	var missing []string
	for _, required := range providers {
		if !registered[strings.ToLower(required)] {
			missing = append(missing, required)
		}
	}
	return missing, nil
}

func (o *ReconcileExternalSubscriptionOptions) newClients(credential azcore.TokenCredential) error {
	var err error
	o.subscriptions, err = armsubscriptions.NewClient(credential, o.clientOptions)
	if err != nil {
		return fmt.Errorf("create subscriptions client: %w", err)
	}
	o.providers, err = armresources.NewProvidersClient(o.SubscriptionID, credential, o.clientOptions)
	if err != nil {
		return fmt.Errorf("create providers client: %w", err)
	}
	o.deployments, err = armdeployments.NewDeploymentsClient(o.SubscriptionID, credential, o.clientOptions)
	if err != nil {
		return fmt.Errorf("create deployments client: %w", err)
	}
	o.graph, err = msgraph.NewGraphServiceClientWithCredentials(credential, []string{graph.Scope})
	if err != nil {
		return fmt.Errorf("create Graph client: %w", err)
	}
	return nil
}

func (o *ReconcileExternalSubscriptionOptions) selectSubscription(ctx context.Context) (externalSelection, externalAccount, error) {
	response, err := o.subscriptions.Get(ctx, o.SubscriptionID, nil)
	if err != nil {
		return externalSelection{}, externalAccount{}, fmt.Errorf("subscription/authentication preflight: %w", err)
	}
	account := externalAccount{ID: ptr.Deref(response.SubscriptionID, ""), Name: ptr.Deref(response.DisplayName, ""), TenantID: ptr.Deref(response.TenantID, ""), State: string(ptr.Deref(response.State, armsubscriptions.SubscriptionState("")))}
	if !strings.EqualFold(account.ID, o.SubscriptionID) {
		return externalSelection{}, account, fmt.Errorf("Azure returned subscription %q instead of explicit target %s", account.ID, o.SubscriptionID)
	}
	selection, err := selectExternalSubscription(o.catalog, account)
	if err != nil {
		return selection, account, err
	}
	if !selection.usesSubscriptionName {
		return selection, account, nil
	}
	resolvedID, err := framework.GetSubscriptionID(ctx, o.subscriptions, account.Name)
	if err != nil {
		return selection, account, fmt.Errorf("catalog subscription-name uniqueness preflight: %w", err)
	}
	if !strings.EqualFold(resolvedID, o.SubscriptionID) {
		return selection, account, fmt.Errorf("catalog subscription name %q resolves to %s instead of target %s", account.Name, resolvedID, o.SubscriptionID)
	}
	return selection, account, nil
}

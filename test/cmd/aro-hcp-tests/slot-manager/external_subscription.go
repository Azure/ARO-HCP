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
	"strings"

	"github.com/go-logr/logr"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/e2eidentities"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/mockidentities"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/azureclient"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/subscriptions"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
)

// grantAKSRBAC mirrors `grantAksRbac` in dev-infrastructure/configurations/ci-bot-rbac-dev.tmpl.bicepparam.
// This command only handles DEV subscriptions.
const grantAKSRBAC = true

const defaultExternalSubscriptionConfigFile = "config/config-dev-ci.yaml"

type RawReconcileExternalSubscriptionOptions struct {
	Subscription string
	ConfigFile   string
	SlotCatalog  string
	Out          io.Writer
}

type validatedReconcileExternalSubscriptionOptions struct {
	*RawReconcileExternalSubscriptionOptions
}

type ValidatedReconcileExternalSubscriptionOptions struct {
	*validatedReconcileExternalSubscriptionOptions
}

type completedReconcileExternalSubscriptionOptions struct {
	Subscription string
	Out          io.Writer
	config       externalSubscriptionConfig
	requests     []assets.PoolRequest
	steps        externalSubscriptionSteps
}

type ReconcileExternalSubscriptionOptions struct {
	*completedReconcileExternalSubscriptionOptions
}

// externalSubscriptionConfig is the part of the DEV CI configuration the command reads.
//
// Reading the configuration directly in Go is a deliberate exception, not a
// pattern to copy. ARO-HCP code normally receives configuration as environment
// variables derived by the pipelines, or through steps in ARO-Tools that do the
// wiring for one specific operation. This command is run by the owners of an
// external subscription, and reads the file so that they do not have to adopt the
// ARO-HCP pipeline DSL.
type externalSubscriptionConfig struct {
	Clouds struct {
		Dev struct {
			Defaults struct {
				CI struct {
					Providers string `yaml:"e2eSubscriptionProviders"`
					Dev       struct {
						Bot struct {
							ApplicationName string `yaml:"applicationName"`
						} `yaml:"bot"`
						MockIdentities struct {
							FirstParty externalApplication `yaml:"firstParty"`
							ARMHelper  externalApplication `yaml:"armHelper"`
							MSIMock    externalApplication `yaml:"msiMock"`
						} `yaml:"mockIdentities"`
					} `yaml:"dev"`
				} `yaml:"ci"`
			} `yaml:"defaults"`
		} `yaml:"dev"`
	} `yaml:"clouds"`
}

type externalApplication struct {
	ApplicationName string `yaml:"applicationName"`
}

// externalSubscriptionSteps are the operations Run sequences. Complete binds them
// to Azure; tests bind them to fakes.
type externalSubscriptionSteps struct {
	resolveSubscriptionID func(ctx context.Context, name string) (string, error)
	registerProviders     func(ctx context.Context, subscriptionIDs, namespaces []string) error
	resolvePrincipalID    func(ctx context.Context, applicationName string) (string, error)
	deploy                func(ctx context.Context, subscriptionIDs []string, deployments ...subscriptions.Deployment) error
	applyContainers       func(ctx context.Context, request assets.PoolRequest) error
	validateContainers    func(ctx context.Context, request assets.PoolRequest) error
	reconcileMockRoles    func(ctx context.Context, request assets.PoolRequest, apply bool) error
}

func DefaultReconcileExternalSubscriptionOptions() *RawReconcileExternalSubscriptionOptions {
	return &RawReconcileExternalSubscriptionOptions{ConfigFile: defaultExternalSubscriptionConfigFile}
}

func BindReconcileExternalSubscriptionOptions(options *RawReconcileExternalSubscriptionOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.Subscription, "subscription", options.Subscription, "Display name of the external E2E subscription, as declared in the slot catalog.")
	command.Flags().StringVar(&options.ConfigFile, "config-file", options.ConfigFile, "Path to the DEV CI configuration. A relative path is resolved from the current directory, so the default only works from the repository root.")
	command.Flags().StringVar(&options.SlotCatalog, "slot-catalog", options.SlotCatalog, "Path to the canonical E2E slot catalog.")
	return nil
}

func newReconcileExternalSubscriptionCommand() (*cobra.Command, error) {
	options := DefaultReconcileExternalSubscriptionOptions()
	command := &cobra.Command{
		Use:   "reconcile-external-subscription",
		Short: "Prepare an externally owned DEV E2E subscription: providers, subscription RBAC, E2E identity containers and slot mock grants.",
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
	switch {
	case strings.TrimSpace(o.Subscription) == "":
		return nil, fmt.Errorf("--subscription must not be empty")
	case o.ConfigFile == "":
		return nil, fmt.Errorf("--config-file must not be empty")
	}
	return &ValidatedReconcileExternalSubscriptionOptions{
		validatedReconcileExternalSubscriptionOptions: &validatedReconcileExternalSubscriptionOptions{RawReconcileExternalSubscriptionOptions: o},
	}, nil
}

func (o *ValidatedReconcileExternalSubscriptionOptions) Complete(_ context.Context) (*ReconcileExternalSubscriptionOptions, error) {
	completed, err := o.loadSelection()
	if err != nil {
		return nil, err
	}
	completed.steps, err = azureExternalSubscriptionSteps()
	if err != nil {
		return nil, err
	}
	return &ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: completed}, nil
}

// loadSelection reads the configuration and catalog and selects the subscription's
// pools, without contacting Azure.
func (o *ValidatedReconcileExternalSubscriptionOptions) loadSelection() (*completedReconcileExternalSubscriptionOptions, error) {
	data, err := os.ReadFile(o.ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("read DEV CI configuration: %w", err)
	}
	var config externalSubscriptionConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode DEV CI configuration %q: %w", o.ConfigFile, err)
	}
	ci := config.Clouds.Dev.Defaults.CI
	if ci.Providers == "" || ci.Dev.Bot.ApplicationName == "" || ci.Dev.MockIdentities.FirstParty.ApplicationName == "" ||
		ci.Dev.MockIdentities.ARMHelper.ApplicationName == "" || ci.Dev.MockIdentities.MSIMock.ApplicationName == "" {
		return nil, fmt.Errorf("DEV CI configuration %q must declare the e2e providers, the bot and the three shared mock applications", o.ConfigFile)
	}
	slotCatalog, err := catalog.LoadCatalog(o.SlotCatalog)
	if err != nil {
		return nil, err
	}
	requests, err := selectExternalPools(slotCatalog, o.Subscription)
	if err != nil {
		return nil, err
	}
	inventories, err := slotCatalog.AssetInventories()
	if err != nil {
		return nil, err
	}
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	for i := range requests {
		requests[i].Out = out
		requests[i].AssetInventories = inventories
	}
	return &completedReconcileExternalSubscriptionOptions{
		Subscription: strings.TrimSpace(o.Subscription),
		Out:          out,
		config:       config,
		requests:     requests,
	}, nil
}

// selectExternalPools returns the pool request per slot environment for every
// pool that uses the named subscription.
func selectExternalPools(slotCatalog *catalog.Catalog, subscription string) ([]assets.PoolRequest, error) {
	subscription = strings.TrimSpace(subscription)
	var requests []assets.PoolRequest
	for _, environment := range slotCatalog.EnvironmentNames() {
		request := assets.PoolRequest{Environment: environment, IncludeUnmanaged: true}
		for _, pool := range slotCatalog.Environments[environment].Pools {
			if pool.Subscriptions.E2E != subscription {
				continue
			}
			if environment != "dev" && environment != "dev-xs" {
				return nil, fmt.Errorf("pool %s/%s uses subscription %q but only dev and dev-xs pools are supported", environment, pool.Name, subscription)
			}
			if !pool.IsUnmanaged() {
				return nil, fmt.Errorf("pool %s/%s is not an unmanaged E2E identity pool; its subscription is prepared by the dev-ci rollout", environment, pool.Name)
			}
			request.Pools = append(request.Pools, pool)
		}
		if len(request.Pools) > 0 {
			requests = append(requests, request)
		}
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("no dev or dev-xs pool uses subscription %q; add it to the slot catalog first", subscription)
	}
	return requests, nil
}

func azureExternalSubscriptionSteps() (externalSubscriptionSteps, error) {
	testContext := framework.NewTestContext()
	credential, err := testContext.AzureCredential()
	if err != nil {
		return externalSubscriptionSteps{}, fmt.Errorf("failed getting Azure credential: %w", err)
	}
	subscriptionClientFactory, err := testContext.GetARMSubscriptionsClientFactory()
	if err != nil {
		return externalSubscriptionSteps{}, fmt.Errorf("failed getting ARM subscriptions client factory: %w", err)
	}
	graphClient, err := msgraph.NewGraphServiceClientWithCredentials(credential, []string{graph.Scope})
	if err != nil {
		return externalSubscriptionSteps{}, fmt.Errorf("create Graph client: %w", err)
	}
	deployer := subscriptions.NewDeployer(credential, azureclient.ARMClientOptions())
	e2eHandler := e2eidentities.NewHandler()
	mockHandler := mockidentities.NewHandler()
	return externalSubscriptionSteps{
		resolveSubscriptionID: func(ctx context.Context, name string) (string, error) {
			return framework.GetSubscriptionID(ctx, subscriptionClientFactory.NewClient(), name)
		},
		registerProviders: func(ctx context.Context, ids, namespaces []string) error {
			return subscriptions.RegisterProviders(ctx, credential, azureclient.ARMClientOptions(), ids, namespaces)
		},
		resolvePrincipalID: func(ctx context.Context, applicationName string) (string, error) {
			return subscriptions.ResolveServicePrincipalID(ctx, graphClient, applicationName)
		},
		deploy:             deployer.Deploy,
		applyContainers:    e2eHandler.ApplyPools,
		validateContainers: e2eHandler.ValidatePools,
		reconcileMockRoles: mockHandler.ReconcileSubscriptionRoles,
	}, nil
}

// Run prepares the subscription. Every step is idempotent, so a failed run can
// simply be repeated. Steps are not all additive: applying the E2E identity
// containers deletes containers that the catalog no longer declares (deployment
// stacks use delete on unmanage), so capacity must be drained before it is lowered.
func (o *ReconcileExternalSubscriptionOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	steps := o.steps

	subscriptionID, err := steps.resolveSubscriptionID(ctx, o.Subscription)
	if err != nil {
		return fmt.Errorf("resolve subscription %q: %w", o.Subscription, err)
	}
	subscriptionIDs := []string{subscriptionID}
	logger.Info("Preparing external subscription", "subscription", o.Subscription, "subscriptionID", subscriptionID, "environments", len(o.requests))

	ci := o.config.Clouds.Dev.Defaults.CI
	if err := steps.registerProviders(ctx, subscriptionIDs, subscriptions.ParseProviderNamespaces(ci.Providers)); err != nil {
		return err
	}

	var principals [4]string
	for i, application := range []string{ci.Dev.Bot.ApplicationName, ci.Dev.MockIdentities.FirstParty.ApplicationName, ci.Dev.MockIdentities.ARMHelper.ApplicationName, ci.Dev.MockIdentities.MSIMock.ApplicationName} {
		if principals[i], err = steps.resolvePrincipalID(ctx, application); err != nil {
			return fmt.Errorf("resolve shared identity (the shared DEV applications are prepared by the dev-ci privileged rollout): %w", err)
		}
	}
	botRBAC := subscriptions.CIBotRBAC(principals[0], subscriptions.CIBotOptions{GrantAKSRBAC: grantAKSRBAC})
	mockRBAC := subscriptions.MockRBAC(subscriptions.MockPrincipals{FirstParty: principals[1], ARMHelper: principals[2], MSIMock: principals[3]})
	if err := steps.deploy(ctx, subscriptionIDs, botRBAC, mockRBAC); err != nil {
		return err
	}

	for _, request := range o.requests {
		if err := steps.applyContainers(ctx, request); err != nil {
			return fmt.Errorf("apply E2E identity containers for %s: %w", request.Environment, err)
		}
	}
	for _, request := range o.requests {
		if err := steps.reconcileMockRoles(ctx, request, true); err != nil {
			return fmt.Errorf("grant slot mock identities for %s: %w", request.Environment, err)
		}
	}

	for _, request := range o.requests {
		if err := steps.validateContainers(ctx, request); err != nil {
			return fmt.Errorf("validate E2E identity containers for %s: %w", request.Environment, err)
		}
	}
	for _, request := range o.requests {
		if err := steps.reconcileMockRoles(ctx, request, false); err != nil {
			return fmt.Errorf("validate slot mock identity grants for %s: %w", request.Environment, err)
		}
	}
	logger.Info("External subscription is ready", "subscription", o.Subscription, "subscriptionID", subscriptionID)
	return nil
}

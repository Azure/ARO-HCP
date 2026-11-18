package registration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armfeatures"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v3"
)

type Configuration map[string]Provider

type Provider struct {
	Poll     bool      `yaml:"poll"`
	Features []Feature `yaml:"features"`
}

type Feature struct {
	Name string `yaml:"name"`
	Poll bool   `yaml:"poll"`
}

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawOptions:    &cmdutils.RawOptions{},
		PollFrequency: 10 * time.Second,
		PollDuration:  5 * time.Minute,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription", opts.SubscriptionID, "Subscription ID in which registration should occur.")
	cmd.Flags().StringVar(&opts.ConfigJSON, "config", opts.ConfigJSON, "Configuration for what to register, encoded as a JSON string.")
	cmd.Flags().DurationVar(&opts.PollFrequency, "poll-frequency", opts.PollFrequency, "Poll frequency while waiting for providers or features to be registered.")
	cmd.Flags().DurationVar(&opts.PollDuration, "poll-duration", opts.PollDuration, "Poll duration to wait for any given provider or feature to be registered.")
	return cmdutils.BindOptions(opts.RawOptions, cmd)
}

// RawOptions holds input values.
type RawOptions struct {
	*cmdutils.RawOptions
	ConfigJSON     string
	SubscriptionID string
	PollFrequency  time.Duration
	PollDuration   time.Duration
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
	*cmdutils.ValidatedOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before Config generation can be invoked.
type completedOptions struct {
	Config Configuration

	ProvidersClient *armresources.ProvidersClient
	FeaturesClient  *armfeatures.Client

	PollFrequency time.Duration
	PollDuration  time.Duration
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "subscription", name: "Azure subscription ID", value: &o.SubscriptionID},
		{flag: "config", name: "registration configuration", value: &o.ConfigJSON},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	validated, err := o.RawOptions.Validate()
	if err != nil {
		return nil, err
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions:       o,
			ValidatedOptions: validated,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	var config Configuration
	if err := yaml.Unmarshal([]byte(o.ConfigJSON), &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal configuration: %w", err)
	}

	completed, err := o.ValidatedOptions.Complete()
	if err != nil {
		return nil, err
	}

	creds, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credentials: %w", err)
	}

	clientOpts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: completed.Configuration,
		},
	}

	providersClient, err := armresources.NewProvidersClient(o.SubscriptionID, creds, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create providers client: %w", err)
	}

	featuresClient, err := armfeatures.NewClient(o.SubscriptionID, creds, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create features client: %w", err)
	}

	return &Options{
		completedOptions: &completedOptions{
			Config:          config,
			ProvidersClient: providersClient,
			FeaturesClient:  featuresClient,
			PollFrequency:   o.PollFrequency,
			PollDuration:    o.PollDuration,
		},
	}, nil
}

func (opts *Options) Register(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	var errs []error
	for provider, cfg := range opts.Config {
		providerLogger := logger.WithValues("provider", provider)
		providerError := opts.ensureProviderRegistered(providerLogger, ctx, provider, cfg)
		var providerSkipped *SkippedRegistrationWait
		if errors.As(providerError, &providerSkipped) {
			// if the RP is not yet registered, but that's because we skipped waiting for it, let's continue on to other
			// RPs but not try to add any features for this one
			continue
		}
		if providerError != nil {
			// let's continue on to other providers, best-effort
			errs = append(errs, providerError)
			continue
		}

		for _, feature := range cfg.Features {
			featureLogger := providerLogger.WithValues("feature", feature.Name)
			featureError := opts.ensureFeatureRegistered(featureLogger, ctx, provider, feature)
			var featureSkipped *SkippedRegistrationWait
			if errors.As(featureError, &featureSkipped) {
				// if the feature is not yet registered, but that's because we skipped waiting for it, let's continue on
				continue
			}
			if featureError != nil {
				// let's continue on to other providers, best-effort
				errs = append(errs, featureError)
				continue
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("registration errors: %v", errs)
	}

	logger.Info("Finished registering providers and features.")
	return nil
}

func (opts *Options) ensureProviderRegistered(logger logr.Logger, ctx context.Context, provider string, cfg Provider) error {
	logger.Info("Ensuring provider registration.")

	existingProviderRegistrationStatus, err := opts.ProvidersClient.Get(ctx, provider, nil)
	if err != nil {
		return fmt.Errorf("failed to get existing registration status for provider %s: %w", provider, err)
	}
	if providerRegistered(existingProviderRegistrationStatus.Provider) {
		logger.Info("Provider already registered.")
		return nil
	}

	logger.Info("Provider registration necessary.")

	providerStatus, err := opts.ProvidersClient.Register(ctx, provider, nil)
	if err != nil {
		return fmt.Errorf("failed to register provider %s: %w", provider, err)
	}

	if providerRegistered(providerStatus.Provider) {
		logger.Info("Provider registered.")
		return nil
	}

	if !cfg.Poll {
		logger.Info("Provider not registered, but polling disabled.")
		return &SkippedRegistrationWait{What: provider}
	}

	var currentStatus string
	logger.Info("Waiting for provider to be registered.", "duration", opts.PollDuration, "frequency", opts.PollFrequency)
	pollCtx, cancel := context.WithTimeout(ctx, opts.PollDuration)
	defer cancel()
	ticker := time.NewTicker(opts.PollFrequency)
	for {
		select {
		case <-ticker.C:
			currentProviderStatus, err := opts.ProvidersClient.Get(ctx, provider, nil)
			if err != nil {
				return fmt.Errorf("failed to get registration status for provider %s: %w", provider, err)
			}
			if providerRegistered(currentProviderStatus.Provider) {
				logger.Info("Provider already registered.")
				return nil
			}
			if currentProviderStatus.RegistrationState != nil && *currentProviderStatus.RegistrationState != currentStatus {
				logger.Info("Observed provider registration state.", "state", *currentProviderStatus.RegistrationState)
				currentStatus = *currentProviderStatus.RegistrationState
			}
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for provider registration status to change: %w", pollCtx.Err())
		}
	}
}

type SkippedRegistrationWait struct {
	What string
}

func (w *SkippedRegistrationWait) Error() string {
	return fmt.Sprintf("skipped registration waiting for %s", w.What)
}

func providerRegistered(provider armresources.Provider) bool {
	return provider.RegistrationState != nil && *provider.RegistrationState == "Registered"
}

func (opts *Options) ensureFeatureRegistered(logger logr.Logger, ctx context.Context, provider string, feature Feature) error {
	logger.Info("Ensuring feature registration.")

	existingFeatureRegistrationStatus, err := opts.FeaturesClient.Get(ctx, provider, feature.Name, nil)
	if err != nil {
		return fmt.Errorf("failed to get existing registration status for feature %s/%s: %w", provider, feature.Name, err)
	}
	if isFeatureRegistered(existingFeatureRegistrationStatus.FeatureResult) {
		logger.Info("Feature already registered.")
		return nil
	}

	logger.Info("Feature registration necessary.")

	featureStatus, err := opts.FeaturesClient.Register(ctx, provider, feature.Name, nil)
	if err != nil {
		return fmt.Errorf("failed to register feature %s/%s: %w", provider, feature.Name, err)
	}

	if isFeatureRegistered(featureStatus.FeatureResult) {
		logger.Info("Feature registered.")
		return nil
	}

	if !feature.Poll {
		logger.Info("Feature not registered, but polling disabled.")
		return &SkippedRegistrationWait{What: fmt.Sprintf("%s/%s", provider, feature.Name)}
	}

	var currentStatus string
	logger.Info("Waiting for feature to be registered.", "duration", opts.PollDuration, "frequency", opts.PollFrequency)
	pollCtx, cancel := context.WithTimeout(ctx, opts.PollDuration)
	defer cancel()
	ticker := time.NewTicker(opts.PollFrequency)
	for {
		select {
		case <-ticker.C:
			currentFeatureStatus, err := opts.FeaturesClient.Get(ctx, provider, feature.Name, nil)
			if err != nil {
				return fmt.Errorf("failed to get registration status for feature %s/%s: %w", provider, feature.Name, err)
			}
			if isFeatureRegistered(currentFeatureStatus.FeatureResult) {
				logger.Info("Feature already registered.")
				return nil
			}
			if currentFeatureStatus.Properties.State != nil && *currentFeatureStatus.Properties.State != currentStatus {
				logger.Info("Observed feature registration state.", "state", *currentFeatureStatus.Properties.State)
				currentStatus = *currentFeatureStatus.Properties.State
			}
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for feature %s/%s registration status to change: %w", provider, feature.Name, pollCtx.Err())
		}
	}
}

func isFeatureRegistered(feature armfeatures.FeatureResult) bool {
	return feature.Properties != nil && feature.Properties.State != nil && *feature.Properties.State == "Registered"
}

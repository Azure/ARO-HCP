package cmdutils

import (
	"fmt"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "Cloud in which the subscription is created.")
	return nil
}

// RawOptions holds input values.
type RawOptions struct {
	Cloud string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before Config generation can be invoked.
type completedOptions struct {
	Cloud         RolloutCloud
	Configuration cloud.Configuration
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

type RolloutCloud string

const (
	RolloutCloudDev      RolloutCloud = "dev"
	RolloutCloudPublic   RolloutCloud = "public"
	RolloutCloudFairfax  RolloutCloud = "ff"
	RolloutCloudMooncake RolloutCloud = "mc"
)

func RolloutClouds() sets.Set[RolloutCloud] {
	return sets.New[RolloutCloud](
		RolloutCloudDev,
		RolloutCloudPublic,
		RolloutCloudFairfax,
		RolloutCloudMooncake,
	)
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "cloud", name: "Azure cloud name", value: &o.Cloud},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	if !RolloutClouds().Has(RolloutCloud(o.Cloud)) {
		return nil, fmt.Errorf("invalid cloud %q, expected one of %v", o.Cloud, RolloutClouds().UnsortedList())
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	var configuration cloud.Configuration
	switch RolloutCloud(o.Cloud) {
	case RolloutCloudDev:
		configuration = cloud.AzurePublic
	case RolloutCloudPublic:
		configuration = cloud.AzurePublic
	case RolloutCloudFairfax:
		configuration = cloud.AzureGovernment
	case RolloutCloudMooncake:
		configuration = cloud.AzureChina
	}

	return &Options{
		completedOptions: &completedOptions{
			Cloud:         RolloutCloud(o.Cloud),
			Configuration: configuration,
		},
	}, nil
}

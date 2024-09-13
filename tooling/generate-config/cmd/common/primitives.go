package common

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	EnvironmentDevelopment string = "dev"
	EnvironmentIntegration string = "int"
	EnvironmentStaging     string = "stg"
	EnvironmentProduction  string = "prod"
)

var usernameRegex = regexp.MustCompile(`[a-zA-Z0-9]+`)

func DefaultPrimitiveOptions() *RawPrimitiveOptions {
	return &RawPrimitiveOptions{}
}

func BindPrimitiveOptions(options *RawPrimitiveOptions, flags *pflag.FlagSet) {
	flags.StringVar(&options.Region, "region", options.Region, "The Azure region to deploy to.")
	flags.StringVar(&options.Environment, "environment", options.Environment, "The ARO-HCP environment to deploy to.")
	flags.StringVar(&options.Username, "username", options.Username, "Your username, if deploying to a development environment.")
}

// RawPrimitiveOptions holds input values.
type RawPrimitiveOptions struct {
	Region      string
	Environment string
	Username    string
}

func (o *RawPrimitiveOptions) Validate() (*ValidatedPrimitiveOptions, error) {
	allEnvironments := sets.New[string](EnvironmentDevelopment, EnvironmentIntegration, EnvironmentStaging, EnvironmentProduction)
	if !allEnvironments.Has(o.Environment) {
		return nil, fmt.Errorf("unknown environment %q, valid environments: %s", o.Environment, strings.Join(allEnvironments.UnsortedList(), ", "))
	}

	if o.Environment == EnvironmentDevelopment && o.Username == "" {
		return nil, fmt.Errorf("a username is required for the developemnt environment")
	}

	if o.Environment == EnvironmentDevelopment && !usernameRegex.MatchString(o.Username) {
		return nil, fmt.Errorf("invalid username %q, must match regex %q", o.Username, usernameRegex.String())
	}

	if o.Environment != EnvironmentDevelopment && o.Username != "" {
		return nil, fmt.Errorf("a username cannot be proviuded for non-development environments")
	}

	if o.Region == "" {
		return nil, fmt.Errorf("a region is required")
	}

	return &ValidatedPrimitiveOptions{
		validatedPrimitiveOptions: &validatedPrimitiveOptions{
			RawPrimitiveOptions: o,
		},
	}, nil
}

// validatedPrimitiveOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedPrimitiveOptions struct {
	*RawPrimitiveOptions
}

type ValidatedPrimitiveOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedPrimitiveOptions
}

func (o *ValidatedPrimitiveOptions) Complete() (*PrimitiveOptions, error) {
	suffix := o.Environment
	if o.Environment == EnvironmentDevelopment {
		suffix += "-" + o.Username
	}

	return &PrimitiveOptions{
		completedPrimitiveOptions: &completedPrimitiveOptions{
			Region:      o.Region,
			Environment: suffix,
			Suffix:      suffix,
		},
	}, nil
}

// completedPrimitiveOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedPrimitiveOptions struct {
	Region      string
	Suffix      string
	Environment string
}

type PrimitiveOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedPrimitiveOptions
}

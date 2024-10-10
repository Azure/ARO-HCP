package main

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/Azure/ARO-HCP/tooling/templatize/config"
)

func DefaultGenerationOptions() *GenerationOptions {
	return &GenerationOptions{}
}

type GenerationOptions struct {
	ConfigFile string
	Input      string
	Cloud      string
	DeployEnv  string
	Region     string
	User       string
}

func (opts *GenerationOptions) Validate() error {
	var errs []error
	err := opts.validateFileExists("config-file", opts.ConfigFile)
	if err != nil {
		errs = append(errs, err)
	}
	err = opts.validateFileExists("input", opts.Input)
	if err != nil {
		errs = append(errs, err)
	}
	if len(opts.DeployEnv) == 0 {
		errs = append(errs, fmt.Errorf("parameter region is missing"))
	}
	if len(opts.Region) == 0 {
		errs = append(errs, fmt.Errorf("parameter deploy-env is missing"))
	}

	// validate cloud
	if len(opts.Cloud) == 0 {
		errs = append(errs, fmt.Errorf("parameter cloud is missing"))
	} else {
		clouds := []string{"public", "fairfax"}
		found := false
		for _, c := range clouds {
			if c == opts.Cloud {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("parameter cloud must be one of %v", clouds))
		}
	}
	return errors.NewAggregate(errs)
}

func (opts *GenerationOptions) validateFileExists(param, path string) error {
	if len(path) == 0 {
		return fmt.Errorf("parameter %s is missing", param)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file %s for parameter %s does not exist", path, param)
	}
	return nil
}

func BindGenerationOptions(opts *GenerationOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "config file path")
	flags.StringVar(&opts.Input, "input", opts.Input, "input file path")
	flags.StringVar(&opts.Cloud, "cloud", opts.Cloud, "the cloud")
	flags.StringVar(&opts.DeployEnv, "deploy-env", opts.DeployEnv, "the deploy environment")
	flags.StringVar(&opts.Region, "region", opts.Region, "resources location")
	flags.StringVar(&opts.User, "user", opts.User, "unique user name")
}

func main() {
	cmd := &cobra.Command{}

	opts := DefaultGenerationOptions()
	BindGenerationOptions(opts, cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}

		println("Config:", opts.ConfigFile)
		println("Input:", opts.Input)
		println("Cloud:", opts.Cloud)
		println("Deployment Env:", opts.DeployEnv)
		println("Region:", opts.Region)
		println("User:", opts.User)

		// TODO: implement templatize tooling
		cfg := config.NewConfigProvider(opts.ConfigFile, opts.Region, opts.User)
		vars, err := cfg.GetVariables(cmd.Context(), opts.Cloud, opts.DeployEnv)
		if err != nil {
			return err
		}
		// print the vars
		for k, v := range vars {
			println(k, v)
		}

		return nil
	}

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

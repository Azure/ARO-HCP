package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/Azure/ARO-HCP/tooling/templatize/config"
)

func DefaultGenerationOptions() *GenerationOptions {
	return &GenerationOptions{}
}

type GenerationOptions struct {
	ConfigFile string
	Input      string
	Output     string
	Cloud      string
	DeployEnv  string
	Region     string
	User       string
}

func (opts *GenerationOptions) Validate() error {
	var errs []error
	err := opts.validateFileAvailability("config-file", opts.ConfigFile)
	if err != nil {
		errs = append(errs, err)
	}
	err = opts.validateFileAvailability("input", opts.Input)
	if err != nil {
		errs = append(errs, err)
	}

	// validate cloud
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

	return errors.NewAggregate(errs)
}

func (opts *GenerationOptions) validateFileAvailability(param, path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file %s for parameter %s does not exist", path, param)
		} else if os.IsPermission(err) {
			return fmt.Errorf("no read permission for file %s", path)
		} else {
			return err
		}
	}
	return nil
}

func main() {
	opts := DefaultGenerationOptions()
	cmd := &cobra.Command{
		Use:   "templatize",
		Short: "templatize",
		Long:  "templatize",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}

			println("Config:", opts.ConfigFile)
			println("Input:", opts.Input)
			println("Cloud:", opts.Cloud)
			println("Deployment Env:", opts.DeployEnv)
			println("Region:", opts.Region)
			println("User:", opts.User)

			return opts.ExecuteTemplate(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "config file path")
	cmd.Flags().StringVar(&opts.Input, "input", opts.Input, "input file path")
	cmd.Flags().StringVar(&opts.Output, "output", opts.Output, "output file path")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "the cloud (public, fairfax)")
	cmd.Flags().StringVar(&opts.DeployEnv, "deploy-env", opts.DeployEnv, "the deploy environment")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "resources location")
	cmd.Flags().StringVar(&opts.User, "user", opts.User, "unique user name")

	if err := cmd.MarkFlagFilename("config-file"); err != nil {
		log.Fatalf("Error marking flag 'config-file': %v", err)
	}
	if err := cmd.MarkFlagRequired("config-file"); err != nil {
		log.Fatalf("Error marking flag 'config-file' as required: %v", err)
	}
	if err := cmd.MarkFlagFilename("input"); err != nil {
		log.Fatalf("Error marking flag 'input': %v", err)
	}
	if err := cmd.MarkFlagRequired("input"); err != nil {
		log.Fatalf("Error marking flag 'input' as required: %v", err)
	}
	if err := cmd.MarkFlagFilename("output"); err != nil {
		log.Fatalf("Error marking flag 'input': %v", err)
	}
	if err := cmd.MarkFlagRequired("output"); err != nil {
		log.Fatalf("Error marking flag 'output' as required: %v", err)
	}
	if err := cmd.MarkFlagRequired("cloud"); err != nil {
		log.Fatalf("Error marking flag 'cloud' as required: %v", err)
	}
	if err := cmd.MarkFlagRequired("deploy-env"); err != nil {
		log.Fatalf("Error marking flag 'deploy-env' as required: %v", err)
	}
	if err := cmd.MarkFlagRequired("region"); err != nil {
		log.Fatalf("Error marking flag 'region' as required: %v", err)
	}

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func (opts *GenerationOptions) ExecuteTemplate(ctx context.Context) error {
	cfg := config.NewConfigProvider(opts.ConfigFile, opts.Region, opts.User)
	vars, err := cfg.GetVariables(ctx, opts.Cloud, opts.DeployEnv)
	if err != nil {
		return err
	}
	// print the vars
	for k, v := range vars {
		fmt.Println(k, v)
	}

	fileName := filepath.Base(opts.Input)

	if err := os.MkdirAll(opts.Output, os.ModePerm); err != nil {
		return err
	}

	output, err := os.Create(path.Join(opts.Output, fileName))
	if err != nil {
		return err
	}
	defer output.Close()

	tmpl, err := template.New(fileName).ParseFiles(opts.Input)
	if err != nil {
		return err
	}

	return tmpl.ExecuteTemplate(output, fileName, vars)
}

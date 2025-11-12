// Copyright 2025 Microsoft Corporation
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

package validate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/rand"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/config/ev2config"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/configuration/render"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/settings"
)

func DefaultOptions(outputDir string, url string) *RawOptions {
	return &RawOptions{
		OutputDir:        outputDir,
		CentralRemoteUrl: url,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ServiceConfigFile, "service-config-file", opts.ServiceConfigFile, "Path to the service configuration file.")
	cmd.Flags().StringVar(&opts.DevSettingsFile, "dev-settings-file", opts.DevSettingsFile, "Validate only the combinations present in the settings file, using public production Ev2 contexts.")
	cmd.Flags().StringVar(&opts.DigestFile, "digest-file", opts.DigestFile, "File holding digests of previously-rendered configurations to validate with.")
	cmd.Flags().StringVar(&opts.OutputDir, "output-dir", opts.OutputDir, "Directory to output rendered configurations to.")
	cmd.Flags().StringVar(&opts.CentralRemoteUrl, "central-remote-url", opts.CentralRemoteUrl, "Git URL for the central remote, used to calculate merge-base.")
	cmd.Flags().BoolVar(&opts.Update, "update", opts.Update, "Update the digest file.")

	for _, flag := range []string{
		"service-config-file",
		"dev-settings-file",
		"digest-file",
	} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	if err := cmd.MarkFlagDirname("output-dir"); err != nil {
		return fmt.Errorf("failed to mark output-dir flag as a directory: %w", err)
	}
	return nil
}

// RawOptions holds input values.
type RawOptions struct {
	ServiceConfigFile string
	DevSettingsFile   string
	CentralRemoteUrl  string

	DigestFile string
	OutputDir  string
	Update     bool
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	ServiceConfig config.ConfigProvider
	Digests       *Digests
	DevSettings   *settings.Settings

	CentralRemoteUrl  string
	OutputDir         string
	ServiceConfigFile string
	DigestFile        string
	Update            bool
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
		{flag: "service-config-file", name: "service configuration file", value: &o.ServiceConfigFile},
		{flag: "digest-file", name: "digest file", value: &o.DigestFile},
		{flag: "output-dir", name: "output directory", value: &o.OutputDir},
		{flag: "central-remote-url", name: "central git remote URL", value: &o.OutputDir},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	d, err := LoadDigests(o.DigestFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load digests: %w", err)
	}

	var s *settings.Settings
	if o.DevSettingsFile != "" {
		var loadErr error
		s, loadErr = settings.Load(o.DevSettingsFile)
		if loadErr != nil {
			return nil, fmt.Errorf("failed to load dev settings: %w", loadErr)
		}
	}

	c, err := config.NewConfigProvider(o.ServiceConfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config file: %w", err)
	}

	return &Options{
		completedOptions: &completedOptions{
			ServiceConfig:     c,
			ServiceConfigFile: o.ServiceConfigFile,
			DevSettings:       s,
			CentralRemoteUrl:  o.CentralRemoteUrl,
			Digests:           d,
			OutputDir:         o.OutputDir,
			DigestFile:        o.DigestFile,
			Update:            o.Update,
		},
	}, nil
}

func (opts *Options) ValidateServiceConfig(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}
	contexts := map[string]map[string][]RegionContext{}
	if opts.DevSettings == nil {
		// if we're validating production environments, we would like to validate all possible regions
		ev2Contexts, err := ev2config.AllContexts()
		if err != nil {
			return fmt.Errorf("failed to load ev2 contexts: %w", err)
		}

		allContexts := opts.ServiceConfig.AllContexts()
		for cloud := range allContexts {
			contexts[cloud] = map[string][]RegionContext{}
			for env := range allContexts[cloud] {
				contexts[cloud][env] = []RegionContext{}
				for _, region := range ev2Contexts[cloud] {
					contexts[cloud][env] = append(contexts[cloud][env], RegionContext{
						Region: region,
						Stamp:  999, // we want to validate that all of our names still work, even with large stamp numbers
					})
				}
			}
		}
	} else {
		// otherwise, we just want to validate the small number of developer defaults
		for _, environment := range opts.DevSettings.Environments {
			envLogger := logger.WithValues("cloud", environment.Defaults.Cloud, "environment", environment.Name)

			cfgContexts := opts.ServiceConfig.AllContexts()
			if _, ok := cfgContexts[environment.Defaults.Cloud]; !ok {
				envLogger.Info("Skipping environment as configuration is missing this cloud.")
				continue
			}
			if _, ok := cfgContexts[environment.Defaults.Cloud][environment.Name]; !ok {
				envLogger.Info("Skipping environment as configuration is missing this environment.")
				continue
			}

			env, err := settings.Resolve(ctx, environment)
			if err != nil {
				return fmt.Errorf("failed to resolve region context: %w", err)
			}

			if _, ok := contexts[env.Cloud]; !ok {
				contexts[env.Cloud] = map[string][]RegionContext{}
			}
			if _, ok := contexts[env.Cloud][env.Environment]; !ok {
				contexts[env.Cloud][env.Environment] = []RegionContext{}
			}

			contexts[env.Cloud][env.Environment] = append(contexts[env.Cloud][env.Environment], RegionContext{
				Region:            env.Region,
				Ev2Cloud:          env.Ev2Cloud,
				RegionShortSuffix: env.RegionShortSuffix,
				Stamp:             env.Stamp,
			})
		}
	}
	return ValidateServiceConfig(
		ctx,
		contexts,
		opts.ServiceConfig,
		opts.ServiceConfigFile,
		opts.Digests,
		opts.OutputDir,
		opts.Update,
		opts.DigestFile,
		opts.CentralRemoteUrl,
	)
}

type RegionContext struct {
	Region            string
	Ev2Cloud          string
	RegionShortSuffix string
	Stamp             int
}

func ValidateServiceConfig(
	ctx context.Context,
	contexts map[string]map[string][]RegionContext,
	serviceConfig config.ConfigProvider,
	serviceConfigFile string,
	digests *Digests,
	outputDir string,
	update bool,
	digestFile string,
	centralRemoteUrl string,
) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	scratchDir := filepath.Join(os.TempDir(), "config-scratch-"+rand.String(8))
	if err := os.MkdirAll(scratchDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create scratch directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(scratchDir); err != nil {
			logger.Error(err, "failed to remove scratch directory")
		}
	}()

	currentDigests := Digests{
		Clouds: map[string]CloudDigests{},
	}
	var jsonSchemaPath string
	for cloud, environments := range contexts {
		if _, ok := currentDigests.Clouds[cloud]; !ok {
			currentDigests.Clouds[cloud] = CloudDigests{
				Environments: map[string]EnvironmentDigests{},
			}
		}
		for environment, regions := range environments {
			if _, ok := currentDigests.Clouds[cloud].Environments[environment]; !ok {
				currentDigests.Clouds[cloud].Environments[environment] = EnvironmentDigests{
					Regions: map[string]string{},
				}
			}
			for _, regionCtx := range regions {
				region := regionCtx.Region
				prefix := fmt.Sprintf("config[%s][%s][%s]:", cloud, environment, region)

				ev2Cloud := cloud
				if regionCtx.Ev2Cloud != "" {
					ev2Cloud = regionCtx.Ev2Cloud
				}
				ev2Cfg, err := ev2config.ResolveConfig(ev2Cloud, region)
				if err != nil {
					return fmt.Errorf("%s: failed to resolve ev2 config: %w", prefix, err)
				}
				replacements := &config.ConfigReplacements{
					RegionReplacement:      region,
					CloudReplacement:       cloud,
					EnvironmentReplacement: environment,
					StampReplacement:       strconv.Itoa(regionCtx.Stamp),
					Ev2Config:              ev2Cfg,
				}
				for key, into := range map[string]*string{
					"regionShortName": &replacements.RegionShortReplacement,
				} {
					value, err := ev2Cfg.GetByPath(key)
					if err != nil {
						return fmt.Errorf("%s %q not found in ev2 config: %w", prefix, key, err)
					}
					str, ok := value.(string)
					if !ok {
						return fmt.Errorf("%s %q is not a string", prefix, key)
					}
					*into = str
				}
				if regionCtx.RegionShortSuffix != "" {
					replacements.RegionShortReplacement += regionCtx.RegionShortSuffix
				}

				resolver, err := serviceConfig.GetResolver(replacements)
				if err != nil {
					return fmt.Errorf("%s failed to get resolver: %w", prefix, err)
				}

				cfg, err := resolver.GetRegionConfiguration(region)
				if err != nil {
					return fmt.Errorf("%s failed to get region config: %w", prefix, err)
				}

				var schemaResolutionErr error
				jsonSchemaPath, schemaResolutionErr = resolver.SchemaPath()
				if schemaResolutionErr != nil {
					return fmt.Errorf("%s failed to get schema path: %w", prefix, schemaResolutionErr)
				}

				if err := resolver.ValidateSchema(cfg); err != nil {
					return fmt.Errorf("%s resolved region config was invalid: %w", prefix, err)
				}

				encoded, err := yaml.Marshal(cfg)
				if err != nil {
					return fmt.Errorf("failed to marshal configuration: %w", err)
				}

				outputPath := configPath(outputDir, cloud, environment, region)
				if os.MkdirAll(filepath.Dir(outputPath), os.ModePerm) != nil {
					return fmt.Errorf("%s failed to create output directory: %w", prefix, err)
				}

				if err := os.WriteFile(outputPath, encoded, os.ModePerm); err != nil {
					return fmt.Errorf("%s failed to write configuration: %w", prefix, err)
				}

				hash := sha256.New()
				hash.Write(encoded)
				hashBytes := hash.Sum(nil)
				currentDigests.Clouds[cloud].Environments[environment].Regions[region] = hex.EncodeToString(hashBytes)
			}
		}
	}
	for cloud, environments := range digests.Clouds {
		if _, ok := currentDigests.Clouds[cloud]; !ok && !update {
			return fmt.Errorf("digests.clouds: cloud %q present in previous digests, but not current ones", cloud)
		}
		for environment, regions := range environments.Environments {
			if _, ok := currentDigests.Clouds[cloud].Environments[environment]; !ok && !update {
				return fmt.Errorf("digests.clouds[%s].environments: environment %q present in previous digests, but not current ones", cloud, environment)
			}
			for region, digest := range regions.Regions {
				currentDigest, ok := currentDigests.Clouds[cloud].Environments[environment].Regions[region]
				if !ok && !update {
					return fmt.Errorf("digests.clouds[%s].environments[%s].regions: region %q present in previous digests, but not current ones", cloud, environment, region)
				}

				var regionCtx *RegionContext
				for _, candidate := range contexts[cloud][environment] {
					if candidate.Region == region {
						regionCtx = &candidate
					}
				}
				if regionCtx == nil {
					return fmt.Errorf("digests.clouds[%s].environments[%s].regions: region %q missing from configuration", cloud, environment, region)
				}

				if currentDigest != digest {
					if !update {
						if err := renderDiff(
							ctx,
							cloud, environment, *regionCtx,
							serviceConfigFile, jsonSchemaPath,
							centralRemoteUrl, outputDir, scratchDir,
						); err != nil {
							logger.WithValues("cloud", cloud, "environment", environment, "region", region).Error(err, "Failed to render diff.")
						}
						return fmt.Errorf("digests.clouds[%s].environments[%s].regions[%s]: rendered configuration digest %s doesn't match previous digest %s", cloud, environment, region, currentDigest, digest)
					}
				}
			}
		}
	}
	for cloud, environments := range currentDigests.Clouds {
		if _, ok := digests.Clouds[cloud]; !ok && !update {
			return fmt.Errorf("digests.clouds: cloud %q present in current digests, but not previous ones", cloud)
		}
		for environment, regions := range environments.Environments {
			if _, ok := digests.Clouds[cloud].Environments[environment]; !ok && !update {
				return fmt.Errorf("digests.clouds[%s].environments: environment %q present in current digests, but not previous ones", cloud, environment)
			}
			for region := range regions.Regions {
				if _, ok := digests.Clouds[cloud].Environments[environment].Regions[region]; !ok && !update {
					return fmt.Errorf("digests.clouds[%s].environments[%s].regions: region %q present in current digests, but not previous ones", cloud, environment, region)
				}
			}
		}
	}
	if update {
		encoded, err := yaml.Marshal(currentDigests)
		if err != nil {
			return fmt.Errorf("failed to marshal current digests: %w", err)
		}

		if os.WriteFile(digestFile, encoded, os.ModePerm) != nil {
			return fmt.Errorf("failed to write current digests: %w", err)
		}
	}

	return nil
}

func renderDiff(
	ctx context.Context,
	cloud, environment string, regionCtx RegionContext,
	serviceConfigFile string,
	jsonSchemaFile string,
	centralRemoteUrl string,
	outputDir, scratchDir string,
) error {
	region := regionCtx.Region
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}
	logger = logger.WithValues("cloud", cloud, "environment", environment, "region", region)
	dir := filepath.Dir(serviceConfigFile)

	currentConfig := configPath(outputDir, cloud, environment, region)
	previousConfig := configPath(scratchDir, cloud, environment, region)
	if err := os.MkdirAll(filepath.Dir(previousConfig), os.ModePerm); err != nil {
		return fmt.Errorf("failed to create directory for previous config: %w", err)
	}
	// In case we are rendering to a file in the git tree, make a copy of the current output to do a diff
	// with later.
	currentConfigCopy := configPath(scratchDir, cloud, environment, region) + ".bak"
	rawCurrentConfig, err := os.ReadFile(currentConfig)
	if err != nil {
		return fmt.Errorf("failed to read current config: %w", err)
	}
	if err := os.WriteFile(currentConfigCopy, rawCurrentConfig, os.ModePerm); err != nil {
		return fmt.Errorf("failed to write current config copy: %w", err)
	}

	// First, we need to find the upstream ref from which the previous digest was written, to
	// use it as a reference point from which we will generate the previous version of the config
	// and provide the user a diff.
	//
	// Since region names are unique across clouds, we could use something like
	//  $ git blame --porcelain -L /malaysiasouth/,+1 -- config/msft.all.digests.yaml
	// however for the dev cloud, we re-use region names - so that won't work.
	// It's possible that the user has updated their digests in multiple commits on one branch,
	// but that seems not highly likely, so we will assume handling that case is not necessary
	// and use the merge-base between the working branch and the upstream as the reference point.
	//
	// n.b. in GitHub Actions CI, we're in some non-standard git state and are better off consuming
	// the upstream ref directly
	mergeBase, err := DetermineMergeBase(ctx, dir, centralRemoteUrl)
	if err != nil {
		return err
	}

	// Now that we have the merge-base, we need to get a copy of that config file - but first, we need to
	// clean up the working state of the repo so we don't clobber anything. We start by stashing anything
	// that's pending.
	if _, err := command(ctx, dir, "git", "stash"); err != nil {
		return fmt.Errorf("failed to stash: %w", err)
	}

	// Since we've stashed, we need to un-stash before we're done.
	defer func() {
		if _, err := command(ctx, dir, "git", "stash", "pop"); err != nil && !strings.Contains(err.Error(), "No stash entries found") {
			logger.Error(err, "failed to pop stash")
		}
	}()

	// With a clean slate, we can check out the reference version of the config file.
	configFile := filepath.Base(serviceConfigFile)
	schemaFile := filepath.Base(jsonSchemaFile)
	if _, err := command(ctx, dir, "git", "checkout", mergeBase, "--", configFile, schemaFile); err != nil {
		return fmt.Errorf("failed to reset service config file to the merge base: %w", err)
	}

	// Since we've edited the config file, we need to reset it before we exit.
	defer func() {
		if _, err := command(ctx, dir, "git", "checkout", "HEAD", "--", configFile, schemaFile); err != nil {
			logger.Error(err, "failed to unstage service config file")
		}
	}()

	// Now we can finally render the previous version of the config.
	opts := render.RawOptions{
		ServiceConfigFile: serviceConfigFile,
		Cloud:             cloud,
		Environment:       environment,
		Region:            region,
		Ev2Cloud:          regionCtx.Ev2Cloud,
		RegionShortSuffix: regionCtx.RegionShortSuffix,
		Stamp:             regionCtx.Stamp,
		Output:            previousConfig,
	}
	validated, err := opts.Validate()
	if err != nil {
		return fmt.Errorf("failed to validate render options: %w", err)
	}
	completed, err := validated.Complete()
	if err != nil {
		return fmt.Errorf("failed to complete render options: %w", err)
	}
	if err := completed.RenderServiceConfig(ctx); err != nil {
		return fmt.Errorf("failed to render previous service config: %w", err)
	}

	diffCmd := exec.CommandContext(ctx, "diff", previousConfig, currentConfigCopy)
	diff, err := diffCmd.CombinedOutput()
	var exitErr *exec.ExitError
	isExitErr := err != nil && errors.As(err, &exitErr)
	isUnexpectedErr := exitErr != nil && exitErr.ExitCode() != 1 // diff exit code other than 1 is some error, not a diff
	if err != nil && (!isExitErr || isUnexpectedErr) {
		return fmt.Errorf("failed to run diff: %w; output: %s", err, string(diff))
	}
	logger.Info("Rendering diff for previous config.", "commit", mergeBase)
	if _, err := fmt.Println(string(diff)); err != nil {
		return fmt.Errorf("failed to print diff: %w", err)
	}
	if strings.TrimSpace(string(diff)) == "" {
		logger.Info("No diff found between previous and current config. This usually means some backwards-incompatible change has been made to the rendering code, and it renders the same with the previous config, but renders differently than the previous version of the code did.")
	}
	return nil
}

// DetermineMergeBase determines the merge base between HEAD and what we think the upstream target branch is.
func DetermineMergeBase(ctx context.Context, dir, centralRemoteUrl string) (string, error) {
	var mergeBase string
	if value, set := os.LookupEnv("MERGE_BASE_REF"); set && value != "" {
		mergeBase = value
	} else {
		var upstreamRef string

		// check to see if the branch has an upstream set, if so, prefer this
		upstream, err := command(ctx, dir, "git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if err != nil &&
			!strings.Contains(err.Error(), "fatal: no upstream configured for branch") &&
			!strings.Contains(err.Error(), "fatal: HEAD does not point to a branch") {
			return "", fmt.Errorf("failed to resolve upstream: %w", err)
		}
		upstreamRef = strings.TrimSpace(upstream)

		// unless it's just a version of this branch on the upstream repo
		branch, err := command(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil &&
			!strings.Contains(err.Error(), "fatal: HEAD does not point to a branch") {
			return "", fmt.Errorf("failed to resolve upstream: %w", err)
		}
		branchName := strings.TrimSpace(branch)

		if upstreamRef == "" || strings.HasSuffix(upstreamRef, branchName) {
			// if no upstream set, make a guess based on the remotes and where 99% of merges go
			remotes, err := command(ctx, dir, "git", "remote")
			if err != nil {
				return "", fmt.Errorf("failed to get git remotes: %w", err)
			}
			for _, remoteName := range strings.Split(remotes, "\n") {
				remote := strings.TrimSpace(remoteName)
				if remote == "" {
					continue
				}
				remoteUrl, err := command(ctx, dir, "git", "remote", "get-url", remote)
				if err != nil {
					return "", fmt.Errorf("failed to get git remote URL: %w", err)
				}
				if strings.TrimSpace(remoteUrl) == centralRemoteUrl {
					upstreamRef = remote + "/main"
					break
				}
			}
		}

		if upstreamRef == "" {
			return "", fmt.Errorf("failed to determine upstream branch - no upstream configured and no remote matches %s", centralRemoteUrl)
		}

		mergeBaseFromGit, err := command(ctx, dir, "git", "merge-base", "HEAD", upstreamRef)
		if err != nil {
			return "", fmt.Errorf("failed to resolve merge base: %w", err)
		}
		mergeBase = strings.TrimSpace(mergeBaseFromGit)
	}
	mergeBase = strings.TrimSpace(mergeBase)
	if mergeBase == "" {
		return "", fmt.Errorf("failed to determine merge base")
	}
	return mergeBase, nil
}

func command(ctx context.Context, dir string, command string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run command %s %s: %w; output: %s", command, strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

func configPath(dir, cloud, environment, region string) string {
	return filepath.Join(dir, cloud, environment, region+".yaml")
}

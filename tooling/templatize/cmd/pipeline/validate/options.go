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
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/ev2config"
	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/configuration/validate"
)

func DefaultValidationOptions() *RawValidationOptions {
	return &RawValidationOptions{
		DevMode:          false,
		DevRegion:        "uksouth",
		CentralRemoteUrl: "https://github.com/Azure/ARO-HCP.git",
	}
}

func BindValidationOptions(opts *RawValidationOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ServiceConfigFile, "service-config-file", opts.ServiceConfigFile, "Path to the service configuration file.")
	cmd.Flags().StringVar(&opts.TopologyFile, "topology-config-file", opts.TopologyFile, "Path to the topology configuration file.")
	cmd.Flags().BoolVar(&opts.DevMode, "dev-mode", opts.DevMode, "Validate just one region, using public production Ev2 contexts.")
	cmd.Flags().StringVar(&opts.DevRegion, "dev-region", opts.DevRegion, "Region to use for dev mode validation.")
	cmd.Flags().BoolVar(&opts.OnlyChanged, "only-changed", opts.OnlyChanged, "Validate only pipelines whose files have uncommitted changes.")
	cmd.Flags().StringVar(&opts.CentralRemoteUrl, "central-remote-url", opts.CentralRemoteUrl, "Central remote URL for the repository.")

	for _, flag := range []string{
		"service-config-file",
		"topology-config-file",
	} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

// RawValidationOptions holds input values.
type RawValidationOptions struct {
	ServiceConfigFile string
	TopologyFile      string
	DevMode           bool
	DevRegion         string
	OnlyChanged       bool
	CentralRemoteUrl  string
}

// validatedValidationOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedValidationOptions struct {
	*RawValidationOptions
}

type ValidatedValidationOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedValidationOptions
}

// completedValidationOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedValidationOptions struct {
	Topology         *topology.Topology
	TopologyDir      string
	Config           config.ConfigProvider
	DevMode          bool
	DevRegion        string
	OnlyChanged      bool
	CentralRemoteUrl string
}

type ValidationOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedValidationOptions
}

func (o *RawValidationOptions) Validate() (*ValidatedValidationOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "service-config-file", name: "service configuration file", value: &o.ServiceConfigFile},
		{flag: "topology-config-file", name: "topology configuration file", value: &o.TopologyFile},
		{flag: "central-remote-url", name: "URl for the central git remote", value: &o.CentralRemoteUrl},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	return &ValidatedValidationOptions{
		validatedValidationOptions: &validatedValidationOptions{
			RawValidationOptions: o,
		},
	}, nil
}

func (o *ValidatedValidationOptions) Complete() (*ValidationOptions, error) {
	t, err := topology.Load(o.TopologyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load topology: %w", err)
	}
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate topology: %w", err)
	}

	c, err := config.NewConfigProvider(o.ServiceConfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config file: %w", err)
	}

	return &ValidationOptions{
		completedValidationOptions: &completedValidationOptions{
			Topology:         t,
			TopologyDir:      filepath.Dir(o.TopologyFile),
			Config:           c,
			DevMode:          o.DevMode,
			DevRegion:        o.DevRegion,
			OnlyChanged:      o.OnlyChanged,
			CentralRemoteUrl: o.CentralRemoteUrl,
		},
	}, nil
}

func (opts *ValidationOptions) ValidatePipelineConfigReferences(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	shouldHandleService := func(string) bool {
		return true
	}
	if opts.OnlyChanged {
		changedServices, err := DetermineChangedServices(ctx, opts.CentralRemoteUrl, opts.TopologyDir, opts.Topology)
		if err != nil {
			return fmt.Errorf("failed to determine changed services: %w", err)
		}
		shouldHandleService = func(serviceGroup string) bool {
			return changedServices.Has(serviceGroup)
		}
	}

	ev2Context, err := ev2config.AllContexts()
	if err != nil {
		return fmt.Errorf("failed to load ev2 contexts: %w", err)
	}
	group, _ := errgroup.WithContext(ctx)
	for cloud, environments := range opts.Config.AllContexts() {
		cloudLogger := logger.WithValues("cloud", cloud)
		cloudLogger.V(3).Info("Validating cloud.", "cloud", cloud)
		for environment := range environments {
			envLogger := cloudLogger.WithValues("environment", environment)
			envLogger.V(3).Info("Validating environment.")
			var regions []string
			if opts.DevMode {
				regions = []string{opts.DevRegion}
			} else {
				regions = ev2Context[cloud]
			}
			for _, region := range regions {
				regionLogger := envLogger.WithValues("region", region)
				regionLogger.V(3).Info("Validating region.")
				prefix := fmt.Sprintf("config[%s][%s][%s]:", cloud, environment, region)
				ev2Cloud := cloud
				if opts.DevMode {
					ev2Cloud = "public" // TODO: load from settings
				}
				ev2Cfg, err := ev2config.ResolveConfig(ev2Cloud, region)
				if err != nil {
					return fmt.Errorf("%s failed to get ev2 config: %w", prefix, err)
				}
				replacements := &config.ConfigReplacements{
					RegionReplacement:      region,
					CloudReplacement:       cloud,
					EnvironmentReplacement: environment,
					StampReplacement:       "1",
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

				resolver, err := opts.Config.GetResolver(replacements)
				if err != nil {
					return fmt.Errorf("%s failed to get resolver: %w", prefix, err)
				}

				cfg, err := resolver.GetRegionConfiguration(region)
				if err != nil {
					return fmt.Errorf("%s failed to get region config: %w", prefix, err)
				}

				if err := resolver.ValidateSchema(cfg); err != nil {
					return fmt.Errorf("%s resolved region config was invalid: %w", prefix, err)
				}

				for _, service := range opts.Topology.Services {
					if err := handleService(regionLogger, prefix, group, opts.TopologyDir, service, cfg, shouldHandleService); err != nil {
						return err
					}
				}
			}
		}
	}
	return group.Wait()
}

func handleService(logger logr.Logger, context string, group *errgroup.Group, baseDir string, service topology.Service, cfg configtypes.Configuration, shouldHandleService func(string) bool) error {
	group.Go(func() error {
		if !shouldHandleService(service.ServiceGroup) {
			return nil
		}
		pipeline, err := types.NewPipelineFromFile(filepath.Join(baseDir, service.PipelinePath), cfg)
		if err != nil {
			return fmt.Errorf("%s: %s: failed to parse pipeline %s: %w", context, service.ServiceGroup, service.PipelinePath, err)
		}

		type variableRef struct {
			variable types.Value
			ref      string
		}
		var variables []variableRef
		for i, rg := range pipeline.ResourceGroups {
			for j, step := range rg.Steps {
				switch step.ActionType() {
				case "Shell":
					specificStep, ok := step.(*types.ShellStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.ShellStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					for k, variable := range specificStep.Variables {
						variables = append(variables, variableRef{
							variable: variable.Value,
							ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].variables[%d]", i, j, k),
						})
					}
					variables = append(variables, variableRef{
						variable: specificStep.ShellIdentity,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].shellIdentity", i, j),
					})
				case "ARM":
					specificStep, ok := step.(*types.ARMStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.ARMStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					for k, variable := range specificStep.Variables {
						variables = append(variables, variableRef{
							variable: variable.Value,
							ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].variables[%d]", i, j, k),
						})
					}
				case "DelegateChildZone":
					specificStep, ok := step.(*types.DelegateChildZoneStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.DelegateChildZoneStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					variables = append(variables, variableRef{
						variable: specificStep.ParentZone,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].parentZone", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.ChildZone,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].childZone", i, j),
					})
				case "SetCertificateIssuer":
					specificStep, ok := step.(*types.SetCertificateIssuerStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.SetCertificateIssuerStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					variables = append(variables, variableRef{
						variable: specificStep.VaultBaseUrl,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].vaultBaseUrl", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.Issuer,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].issuer", i, j),
					})
				case "CreateCertificate":
					specificStep, ok := step.(*types.CreateCertificateStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.CreateCertificateStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					variables = append(variables, variableRef{
						variable: specificStep.VaultBaseUrl,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].vaultBaseUrl", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.CertificateName,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].certificateName", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.ContentType,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].contentType", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.SAN,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].san", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.Issuer,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].issuer", i, j),
					})
				case "ResourceProviderRegistration":
					specificStep, ok := step.(*types.ResourceProviderRegistrationStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.ResourceProviderRegistrationStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					variables = append(variables, variableRef{
						variable: specificStep.ResourceProviderNamespaces,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].resourceProviderNamespaces", i, j),
					})
				case "ImageMirror":
					specificStep, ok := step.(*types.ImageMirrorStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.ImageMirrorStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					variables = append(variables, variableRef{
						variable: specificStep.TargetACR,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].targetACR", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.SourceRegistry,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].sourceRegistry", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.Repository,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].repository", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.Digest,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].digest", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.PullSecretKeyVault,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].pullSecretKeyVault", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.PullSecretName,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].pullSecretName", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.ShellIdentity,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].shellIdentity", i, j),
					})
				case "RPLogsAccount", "ClusterLogsAccount":
					specificStep, ok := step.(*types.LogsStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.LogsStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					variables = append(variables, variableRef{
						variable: specificStep.SubscriptionId,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].subscriptionId", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.Namespace,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].namespace", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.CertSAN,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].certsan", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.CertDescription,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].certdescription", i, j),
					})
					variables = append(variables, variableRef{
						variable: specificStep.ConfigVersion,
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].configVersion", i, j),
					})
				case "ProviderFeatureRegistration":
					specificStep, ok := step.(*types.ProviderFeatureRegistrationStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.ProviderFeatureRegistrationStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					variables = append(variables, variableRef{
						variable: types.Value{ConfigRef: specificStep.ProviderConfigRef},
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].providerConfigRef", i, j),
					})
					variables = append(variables, variableRef{
						variable: types.Value{Input: &specificStep.IdentityFrom},
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].identityFrom", i, j),
					})
				case "SecretSync":
					specificStep, ok := step.(*types.SecretSyncStep)
					if !ok {
						return fmt.Errorf("%s: resourceGroups[%d].steps[%d]: have action %q, expected *types.SecretSyncStep, but got %T", service.ServiceGroup, i, j, step.ActionType(), step)
					}
					variables = append(variables, variableRef{
						variable: types.Value{Input: &specificStep.IdentityFrom},
						ref:      fmt.Sprintf("resourceGroups[%d].steps[%d].identityFrom", i, j),
					})
				}
			}
		}
		for _, variable := range variables {
			if variable.variable.ConfigRef != "" {
				if _, err := cfg.GetByPath(variable.variable.ConfigRef); err != nil {
					return fmt.Errorf("%s: %s: %s: configRef %q not present in configuration: %w", context, service.ServiceGroup, variable.ref, variable.variable.ConfigRef, err)
				}
			}
			if variable.variable.Value == "" && variable.variable.ConfigRef == "" && variable.variable.Input.Name == "" && variable.variable.Input.Step == "" {
				return fmt.Errorf("%s: %s: %s: variable is empty", context, service.ServiceGroup, variable.ref)
			}
		}
		logger.V(3).Info("Validated service.", "service", service.ServiceGroup)
		return nil
	})
	for _, child := range service.Children {
		if err := handleService(logger, context, group, baseDir, child, cfg, shouldHandleService); err != nil {
			return err
		}
	}
	return nil
}

// DetermineChangedServices uses `git diff` output to try to guess which services have changes in the working tree.
// Couple of notes:
//   - the topology file itself doesn't have to be at the root of the repo
//   - paths in the topology file are relative to its dir
//   - `git diff` output is relative to the repo root
//
// Therefore, we first compute the relative path from the repo root to the topology file, so that we can prepend it
// to the pipeline paths and figure out where each pipeline is relative to the root of the repo, so we can detect
// diffs that touch it.
//
// Note as well that this approach will only catch diffs that are directly in the pipeline's directory - any shared
// files or other dependencies that change won't be captured. This approach is meant to be a nice utility and best-
// effort, not run in CI, so this is acceptable.
//
// We may choose to improve this algorithm in the future to look for `.bicep` references to paths outside the dir
// or relative paths in the `pipeline.yaml` itself.
func DetermineChangedServices(ctx context.Context, centralRemoteUrl, topologyDir string, t *topology.Topology) (sets.Set[string], error) {
	mergeBase, err := validate.DetermineMergeBase(ctx, topologyDir, centralRemoteUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to determine merge base: %w", err)
	}

	// this will be <repo-root-path>/<topology-relative-path>/topology.yaml
	topologyDirAbsPath, err := filepath.Abs(topologyDir)
	if err != nil {
		return nil, fmt.Errorf("failed to determine absolute path of topology: %w", err)
	}

	// this will be <repo-root-path>
	var gitAbsPath string
	{
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to run git rev-parse: %w; output: %s", err, string(out))
		}
		gitAbsPath = strings.TrimSpace(string(out))
	}

	topologyDirRelPath, err := filepath.Rel(gitAbsPath, topologyDirAbsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to determine relative path of topology: %w", err)
	}

	// these will be relative to the <repo-root-path>
	var files []string
	{
		cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", fmt.Sprintf("%s..HEAD", mergeBase))
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to run git diff: %w; output: %s", err, string(out))
		}
		for _, line := range strings.Split(string(out), "\n") {
			files = append(files, strings.TrimSpace(line))
		}
	}
	w := walker{
		files:              files,
		topologyDirRelPath: topologyDirRelPath,
		changed:            sets.New[string](),
	}
	for _, service := range t.Services {
		w.walk(service)
	}
	return w.changed, nil
}

type walker struct {
	files              []string
	topologyDirRelPath string
	changed            sets.Set[string]
}

func (w *walker) walk(service topology.Service) {
	for _, child := range service.Children {
		w.walk(child)
	}

	serviceDir := filepath.Join(w.topologyDirRelPath, filepath.Dir(service.PipelinePath))

	for _, file := range w.files {
		if strings.HasPrefix(file, serviceDir) {
			w.changed.Insert(service.ServiceGroup)
		}
	}
}

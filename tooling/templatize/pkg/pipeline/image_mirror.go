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

package pipeline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

const (
	imageMirrorMaxRetries    = 3
	imageMirrorRetryInterval = 30 * time.Second
)

func runImageMirrorStep(id graph.Identifier, ctx context.Context, step *types.ImageMirrorStep, options *StepRunOptions, state *ExecutionState, outputWriter io.Writer) error {
	logger := logr.FromContextOrDiscard(ctx)

	// resolve step variables using the same mechanism as shell steps
	resolvedVars, err := resolveImageMirrorVariables(id, step, options, state)
	if err != nil {
		return fmt.Errorf("failed to resolve image mirror variables: %w", err)
	}

	targetACR := resolvedVars["TARGET_ACR"]
	sourceRegistry := resolvedVars["SOURCE_REGISTRY"]
	repository := resolvedVars["REPOSITORY"]
	digest := resolvedVars["DIGEST"]
	pullSecretKV := resolvedVars["PULL_SECRET_KV"]
	pullSecretName := resolvedVars["PULL_SECRET"]

	// shortcut if source and target are the same registry
	acrSuffix, err := getACRDomainSuffix(ctx)
	if err != nil {
		return fmt.Errorf("failed to get ACR domain suffix: %w", err)
	}
	if sourceRegistry == targetACR+acrSuffix {
		logger.Info("Source and target registry are the same, skipping mirror")
		fmt.Fprintln(outputWriter, "Source and target registry are the same. No mirroring needed.")
		return nil
	}

	// dry run check
	if options.DryRun {
		logger.Info("DRY_RUN is enabled, skipping image mirror")
		fmt.Fprintln(outputWriter, "DRY_RUN is enabled. Exiting without making changes.")
		return nil
	}

	// determine source registry credentials
	var sourceCredential auth.Credential
	if isCIRegistry(sourceRegistry) {
		// CI registries (e.g. registry.build02.ci.openshift.org) use oc registry login
		logger.Info("Source registry is a CI registry, using oc registry login", "registry", sourceRegistry)
		fmt.Fprintf(outputWriter, "Setting up registry authentication for CI source registry %s.\n", sourceRegistry)
		sourceCredential, err = getOCRegistryCredential(ctx, sourceRegistry)
		if err != nil {
			return fmt.Errorf("failed to get CI registry credentials via oc: %w", err)
		}
	} else if pullSecretKV != "" && pullSecretName != "" {
		logger.Info("Fetching pull secret from Key Vault", "vault", pullSecretKV, "secret", pullSecretName)
		sourceCredential, err = fetchPullSecretCredential(ctx, pullSecretKV, pullSecretName, sourceRegistry)
		if err != nil {
			return fmt.Errorf("failed to fetch pull secret: %w", err)
		}
		if sourceCredential == auth.EmptyCredential {
			logger.Info("No credentials found in pull secret for source registry, using anonymous access", "registry", sourceRegistry)
			fmt.Fprintf(outputWriter, "No credentials found for %s in pull secret. Using anonymous access.\n", sourceRegistry)
		}
	} else {
		logger.Info("No pull secret configured, using anonymous access for source registry")
	}

	// get target ACR credentials
	logger.Info("Logging into target ACR", "acr", targetACR)
	targetLoginServer := targetACR + acrSuffix
	targetCredential, err := getACRCredential(ctx, targetACR, targetLoginServer)
	if err != nil {
		return fmt.Errorf("failed to get ACR credentials: %w", err)
	}

	// build source and target references
	srcRef := fmt.Sprintf("%s/%s@%s", sourceRegistry, repository, digest)
	digestNoPrefix := strings.TrimPrefix(digest, "sha256:")
	targetRef := fmt.Sprintf("%s/%s:%s", targetLoginServer, repository, digestNoPrefix)

	logger.Info("Mirroring image", "source", srcRef, "target", targetRef)
	fmt.Fprintf(outputWriter, "Mirroring image %s to %s.\n", srcRef, targetRef)
	fmt.Fprintf(outputWriter, "The image will still be available under its original digest %s in the target registry.\n", digest)

	// copy with retries
	if err := copyWithRetry(ctx, logger, srcRef, targetRef, sourceCredential, targetCredential, outputWriter); err != nil {
		return fmt.Errorf("failed to mirror image after %d attempts: %w", imageMirrorMaxRetries, err)
	}

	logger.Info("Image mirrored successfully")
	fmt.Fprintln(outputWriter, "Image mirrored successfully.")
	return nil
}

// resolveImageMirrorVariables resolves the step's variable references (configRef, input)
// to concrete string values, using the same mechanism as shell steps.
func resolveImageMirrorVariables(id graph.Identifier, step *types.ImageMirrorStep, options *StepRunOptions, state *ExecutionState) (map[string]string, error) {
	// build the same variable list that ResolveImageMirrorStep would create
	variables := []types.Variable{
		{Name: "TARGET_ACR", Value: step.TargetACR},
		{Name: "SOURCE_REGISTRY", Value: step.SourceRegistry},
		{Name: "REPOSITORY", Value: step.Repository},
		{Name: "DIGEST", Value: step.Digest},
		{Name: "PULL_SECRET_KV", Value: step.PullSecretKeyVault},
		{Name: "PULL_SECRET", Value: step.PullSecretName},
	}

	state.RLock()
	resolved, err := mapStepVariables(id.ServiceGroup, variables, options.Configuration, state.Outputs)
	state.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve variables: %w", err)
	}

	// validate all required variables are present (pull secret vars are optional for anonymous registries)
	for _, name := range []string{"TARGET_ACR", "SOURCE_REGISTRY", "REPOSITORY", "DIGEST"} {
		if resolved[name] == "" {
			return nil, fmt.Errorf("required variable %s is not set", name)
		}
	}

	return resolved, nil
}

// copyWithRetry attempts to copy an image with retry logic for transient failures.
func copyWithRetry(ctx context.Context, logger logr.Logger, src, dst string, srcCredential, dstCredential auth.Credential, outputWriter io.Writer) error {
	var lastErr error
	for attempt := 1; attempt <= imageMirrorMaxRetries; attempt++ {
		err := copyImage(ctx, src, dst, srcCredential, dstCredential)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < imageMirrorMaxRetries {
			logger.Info("Image copy failed, retrying", "attempt", attempt, "error", err)
			fmt.Fprintf(outputWriter, "Copy failed (attempt %d/%d): %v. Retrying in %v...\n",
				attempt, imageMirrorMaxRetries, err, imageMirrorRetryInterval)
			select {
			case <-time.After(imageMirrorRetryInterval):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return lastErr
}

// copyImage performs the registry-to-registry image copy using oras-go.
func copyImage(ctx context.Context, srcRef, dstRef string, srcCredential, dstCredential auth.Credential) error {
	// parse source reference
	srcParts := strings.SplitN(srcRef, "/", 2)
	if len(srcParts) != 2 {
		return fmt.Errorf("invalid source reference: %s", srcRef)
	}
	srcRegistry := srcParts[0]
	srcRepoAndRef := srcParts[1]

	// parse destination reference
	dstParts := strings.SplitN(dstRef, "/", 2)
	if len(dstParts) != 2 {
		return fmt.Errorf("invalid destination reference: %s", dstRef)
	}
	dstRegistry := dstParts[0]
	dstRepoAndRef := dstParts[1]

	// set up source repository
	srcRepo, err := remote.NewRepository(fmt.Sprintf("%s/%s", srcRegistry, strings.Split(srcRepoAndRef, "@")[0]))
	if err != nil {
		return fmt.Errorf("failed to create source repository: %w", err)
	}
	srcRepo.Client = &auth.Client{
		Credential: func(ctx context.Context, hostport string) (auth.Credential, error) {
			return srcCredential, nil
		},
	}

	// set up destination repository
	dstRepoName := strings.Split(dstRepoAndRef, ":")[0]
	dstRepo, err := remote.NewRepository(fmt.Sprintf("%s/%s", dstRegistry, dstRepoName))
	if err != nil {
		return fmt.Errorf("failed to create destination repository: %w", err)
	}
	dstRepo.Client = &auth.Client{
		Credential: func(ctx context.Context, hostport string) (auth.Credential, error) {
			return dstCredential, nil
		},
	}

	// extract the reference (digest or tag) for the source
	srcReference := ""
	if idx := strings.Index(srcRepoAndRef, "@"); idx != -1 {
		srcReference = srcRepoAndRef[idx+1:]
	}

	// extract the tag for the destination
	dstTag := ""
	if idx := strings.Index(dstRepoAndRef, ":"); idx != -1 {
		dstTag = dstRepoAndRef[idx+1:]
	}

	// copy the image
	desc, err := oras.Copy(ctx, srcRepo, srcReference, dstRepo, dstTag, oras.DefaultCopyOptions)
	if err != nil {
		return fmt.Errorf("failed to copy image: %w", err)
	}
	_ = desc

	return nil
}

// fetchPullSecretCredential fetches a Docker pull secret from Azure Key Vault
// and returns an oras auth credential for the source registry.
func fetchPullSecretCredential(ctx context.Context, vaultName, secretName, registry string) (auth.Credential, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	vaultURL := fmt.Sprintf("https://%s.vault.azure.net", vaultName)
	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to create Key Vault client: %w", err)
	}

	resp, err := client.GetSecret(ctx, secretName, "", nil)
	if err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to get secret %s: %w", secretName, err)
	}

	if resp.Value == nil {
		return auth.EmptyCredential, fmt.Errorf("secret %s is empty", secretName)
	}

	// secret is base64-encoded Docker config JSON
	secretValue := *resp.Value
	decoded, err := base64.StdEncoding.DecodeString(secretValue)
	if err != nil {
		// not base64, assume raw JSON
		decoded = []byte(secretValue)
	}

	// parse Docker config JSON to extract credentials for the registry
	var dockerConfig struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(decoded, &dockerConfig); err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to parse Docker config from secret: %w", err)
	}

	// find auth for the source registry using exact host matching
	for registryHost, regAuth := range dockerConfig.Auths {
		if registryHostMatches(registry, registryHost) {
			authDecoded, err := base64.StdEncoding.DecodeString(regAuth.Auth)
			if err != nil {
				return auth.EmptyCredential, fmt.Errorf("failed to decode auth for %s: %w", registryHost, err)
			}
			parts := strings.SplitN(string(authDecoded), ":", 2)
			if len(parts) != 2 {
				return auth.EmptyCredential, fmt.Errorf("invalid auth format for %s", registryHost)
			}
			return auth.Credential{
				Username: parts[0],
				Password: parts[1],
			}, nil
		}
	}

	// no credentials found for this registry - return empty credential (anonymous access)
	return auth.EmptyCredential, nil
}

// getACRCredential gets an auth credential for an Azure Container Registry
// using the az CLI to get an access token.
func getACRCredential(ctx context.Context, acrName, loginServer string) (auth.Credential, error) {
	cmd := exec.CommandContext(ctx, "az", "acr", "login", "--name", acrName,
		"--expose-token", "--output", "tsv", "--query", "accessToken")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to get ACR access token for %s: %s: %w", acrName, string(output), err)
	}

	return auth.Credential{
		Username: "00000000-0000-0000-0000-000000000000",
		Password: strings.TrimSpace(string(output)),
	}, nil
}

// getACRDomainSuffix returns the ACR domain suffix for the current Azure cloud
// (e.g., ".azurecr.io" for public Azure).
func getACRDomainSuffix(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "az", "cloud", "show",
		"--query", "suffixes.acrLoginServerEndpoint", "--output", "tsv")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get ACR domain suffix: %s: %w", string(output), err)
	}
	return strings.TrimSpace(string(output)), nil
}

// isCIRegistry checks if the source registry is a CI registry that requires
// oc registry login. This is determined by the USE_OC_LOGIN_REGISTRIES env var,
// which is set by the CI provisioning step (aro-hcp-provision-environment).
func isCIRegistry(sourceRegistry string) bool {
	ocLoginRegistries := os.Getenv("USE_OC_LOGIN_REGISTRIES")
	if ocLoginRegistries == "" {
		return false
	}
	for _, registry := range strings.Fields(ocLoginRegistries) {
		if sourceRegistry == registry {
			return true
		}
	}
	return false
}

// getOCRegistryCredential runs "oc registry login" and parses the resulting
// Docker config JSON to extract credentials for the given registry.
func getOCRegistryCredential(ctx context.Context, registry string) (auth.Credential, error) {
	tmpFile, err := os.CreateTemp("", "oc-auth-*.json")
	if err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to create temp file for oc auth: %w", err)
	}
	// oc registry login expects valid JSON in the target file
	if _, err := tmpFile.Write([]byte(`{"auths":{}}`)); err != nil {
		_ = tmpFile.Close()
		return auth.EmptyCredential, fmt.Errorf("failed to initialize oc auth file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to close oc auth file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	cmd := exec.CommandContext(ctx, "oc", "registry", "login", "--to", tmpFile.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		return auth.EmptyCredential, fmt.Errorf("oc registry login failed: %s: %w", string(output), err)
	}

	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to read oc auth file: %w", err)
	}

	var dockerConfig struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(data, &dockerConfig); err != nil {
		return auth.EmptyCredential, fmt.Errorf("failed to parse oc auth config: %w", err)
	}

	for registryHost, regAuth := range dockerConfig.Auths {
		if registryHostMatches(registry, registryHost) {
			authDecoded, err := base64.StdEncoding.DecodeString(regAuth.Auth)
			if err != nil {
				return auth.EmptyCredential, fmt.Errorf("failed to decode auth for %s: %w", registryHost, err)
			}
			parts := strings.SplitN(string(authDecoded), ":", 2)
			if len(parts) != 2 {
				return auth.EmptyCredential, fmt.Errorf("invalid auth format for %s", registryHost)
			}
			return auth.Credential{
				Username: parts[0],
				Password: parts[1],
			}, nil
		}
	}

	return auth.EmptyCredential, fmt.Errorf("oc registry login succeeded but no credentials found for %s", registry)
}

// registryHostMatches compares a registry hostname against a Docker config key,
// normalizing both to host[:port] for exact matching. Docker config keys may
// include schemes (https://quay.io) or paths (https://index.docker.io/v1/).
func registryHostMatches(registry, configKey string) bool {
	return normalizeRegistryHost(registry) == normalizeRegistryHost(configKey)
}

// normalizeRegistryHost extracts the host[:port] from a registry reference,
// stripping any scheme or path components.
func normalizeRegistryHost(registry string) string {
	// if it looks like a URL with a scheme, parse it
	if strings.Contains(registry, "://") {
		if u, err := url.Parse(registry); err == nil {
			return u.Host
		}
	}
	// strip any path component (e.g. "registry.example.com/v1/" -> "registry.example.com")
	if idx := strings.Index(registry, "/"); idx != -1 {
		registry = registry[:idx]
	}
	return registry
}

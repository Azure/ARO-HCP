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

package internal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/docker"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/types"
	"go.uber.org/zap"
)

func Log() *zap.SugaredLogger {
	return zap.L().Sugar()
}

// SyncConfig is the configuration for the image sync
type SyncConfig struct {
	Repositories            []string
	NumberOfTags            int
	Secrets                 []Secrets
	AcrTargetRegistry       string
	TenantId                string
	RequestTimeout          int
	AddLatest               bool
	ManagedIdentityClientID string
}
type Secrets struct {
	Registry   string
	SecretFile string
}

// BearerSecret is the secret for the source OCI registry
type BearerSecret struct {
	BearerToken string
}

// AzureSecret is the token configured in the ACR
type AzureSecretFile struct {
	Username string
	Password string
}

func (a AzureSecretFile) BasicAuthEncoded() string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", a.Username, a.Password)))
}

// Copy copies an image from one registry to another
func Copy(ctx context.Context, dstreference, srcreference string, dstauth, srcauth *types.DockerAuthConfig) error {
	policyctx, err := signature.NewPolicyContext(&signature.Policy{
		Default: signature.PolicyRequirements{
			signature.NewPRInsecureAcceptAnything(),
		},
	})
	if err != nil {
		return err
	}

	src, err := docker.ParseReference("//" + srcreference)
	if err != nil {
		return err
	}

	dst, err := docker.ParseReference("//" + dstreference)
	if err != nil {
		return err
	}

	_, err = copy.Image(ctx, policyctx, dst, src, &copy.Options{
		SourceCtx: &types.SystemContext{
			DockerAuthConfig: srcauth,
		},
		DestinationCtx: &types.SystemContext{
			DockerAuthConfig: dstauth,
		},
	})

	return err
}

func readBearerSecret(filename string) (*BearerSecret, error) {
	secretBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var secret BearerSecret
	err = json.Unmarshal(secretBytes, &secret)
	if err != nil {
		return nil, err
	}

	return &secret, nil
}

func readAzureSecret(filename string) (*AzureSecretFile, error) {
	secretBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var secret AzureSecretFile
	err = json.Unmarshal(secretBytes, &secret)
	if err != nil {
		return nil, err
	}

	return &secret, nil
}

func filterTagsToSync(src, target []string) []string {
	var tagsToSync []string

	targetMap := make(map[string]bool)
	for _, targetTag := range target {
		targetMap[targetTag] = true
	}

	for _, srcTag := range src {
		if _, ok := targetMap[srcTag]; !ok {
			tagsToSync = append(tagsToSync, srcTag)
		}
	}
	return tagsToSync
}

// DoSync syncs the images from the source registry to the target registry
func DoSync(cfg *SyncConfig) error {
	Log().Infow("Syncing images", "images", cfg.Repositories, "numberoftags", cfg.NumberOfTags)
	ctx := context.Background()

	srcRegistries := make(map[string]Registry)
	var err error

	for _, secret := range cfg.Secrets {
		if secret.Registry == "quay.io" {
			quaySecret, err := readBearerSecret(secret.SecretFile)
			if err != nil {
				return fmt.Errorf("error reading secret file: %w %s", err, secret.SecretFile)
			}
			qr := NewQuayRegistry(cfg, quaySecret.BearerToken)
			srcRegistries[secret.Registry] = qr
		} else {
			if strings.HasSuffix(secret.Registry, "azurecr.io") ||
				strings.HasSuffix(secret.Registry, "azurecr.cn") ||
				strings.HasSuffix(secret.Registry, "azurecr.us") {
				azureSecret, err := readAzureSecret(secret.SecretFile)
				if err != nil {
					return fmt.Errorf("error reading azure secret file: %w %s", err, secret.SecretFile)
				}
				bearerSecret, err := getACRBearerToken(ctx, *azureSecret, secret.Registry)
				if err != nil {
					return fmt.Errorf("error getting ACR bearer token: %w", err)
				}
				srcRegistries[secret.Registry] = NewACRWithTokenAuth(cfg, secret.Registry, bearerSecret)
			} else {
				s, err := readBearerSecret(secret.SecretFile)
				bearerSecret := s.BearerToken
				if err != nil {
					return fmt.Errorf("error reading secret file: %w %s", err, secret.SecretFile)
				}
				srcRegistries[secret.Registry] = NewOCIRegistry(cfg, secret.Registry, bearerSecret)
			}
		}
	}

	targetACR := NewAzureContainerRegistry(cfg)
	acrPullSecret, err := targetACR.GetPullSecret(ctx)
	if err != nil {
		return fmt.Errorf("error getting pull secret: %w", err)
	}

	targetACRAuth := types.DockerAuthConfig{Username: "00000000-0000-0000-0000-000000000000", Password: acrPullSecret.RefreshToken}

	for _, repoName := range cfg.Repositories {
		var srcTags, acrTags []string

		baseURL := strings.Split(repoName, "/")[0]
		repoName = strings.Join(strings.Split(repoName, "/")[1:], "/")

		Log().Infow("Syncing repository", "repository", repoName, "baseurl", baseURL)

		if client, ok := srcRegistries[baseURL]; ok {
			srcTags, err = client.GetTags(ctx, repoName)
			if err != nil {
				return fmt.Errorf("error getting tags from %s: %w", baseURL, err)
			}
			Log().Debugw("Got tags from quay", "tags", srcTags)
		} else {
			// No secret defined, create a default client without auth
			oci := NewOCIRegistry(cfg, baseURL, "")
			srcTags, err = oci.GetTags(ctx, repoName)
			if err != nil {
				return fmt.Errorf("error getting oci tags: %w", err)
			}
			Log().Debugw(fmt.Sprintf("Got tags from %s", baseURL), "repo", repoName, "tags", srcTags)
		}

		exists, err := targetACR.RepositoryExists(ctx, repoName)
		if err != nil {
			return fmt.Errorf("error getting ACR repository information: %w", err)
		}

		if exists {
			acrTags, err = targetACR.GetTags(ctx, repoName)
			if err != nil {
				return fmt.Errorf("error getting ACR tags: %w", err)
			}
			Log().Infow("Got tags from acr", "tags", acrTags)
		} else {
			Log().Infow("Repository does not exist", "repository", repoName)
		}

		tagsToSync := filterTagsToSync(srcTags, acrTags)

		Log().Infow("Images to sync", "images", tagsToSync)

		for _, tagToSync := range tagsToSync {
			source := fmt.Sprintf("%s/%s:%s", baseURL, repoName, tagToSync)
			target := fmt.Sprintf("%s/%s:%s", cfg.AcrTargetRegistry, repoName, tagToSync)
			Log().Infow("Copying images", "images", tagToSync, "from", source, "to", target)

			err = Copy(ctx, target, source, &targetACRAuth, nil)
			if err != nil {
				return fmt.Errorf("error copying image: %w", err)
			}
		}

	}
	return nil
}

package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/docker"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/types"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

func Log() *zap.SugaredLogger {
	return zap.L().Sugar()
}

// SyncConfig is the configuration for the image sync
type SyncConfig struct {
	Repositories            []string
	NumberOfTags            int
	QuaySecretFile          string
	AcrRegistry             string
	TenantId                string
	RequestTimeout          int
	AddLatest               bool
	ManagedIdentityClientID string
}

// QuaySecret is the secret for quay.io
type QuaySecret struct {
	BearerToken string
}

// NewSyncConfig creates a new SyncConfig from the configuration file
func NewSyncConfig() *SyncConfig {
	var sc *SyncConfig
	v := viper.GetViper()
	v.SetDefault("numberoftags", 10)
	v.SetDefault("requesttimeout", 10)
	v.SetDefault("addlatest", false)

	if err := v.BindEnv("ManagedIdentityClientId", "MANAGED_IDENTITY_CLIENT_ID"); err != nil {
		Log().Fatalw("Error while binding environment variable %s", err.Error())
	}

	if err := v.Unmarshal(&sc); err != nil {
		Log().Fatalw("Error while unmarshalling configuration %s", err.Error())
	}
	Log().Debugw("Using configuration", "config", sc)
	return sc
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

func readQuaySecret(filename string) (*QuaySecret, error) {
	secretBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var secret QuaySecret
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
func DoSync() error {
	cfg := NewSyncConfig()
	Log().Infow("Syncing images", "images", cfg.Repositories, "numberoftags", cfg.NumberOfTags)
	ctx := context.Background()

	quaySecret, err := readQuaySecret(cfg.QuaySecretFile)
	if err != nil {
		return fmt.Errorf("error reading secret file: %w", err)
	}
	qr := NewQuayRegistry(cfg, quaySecret.BearerToken)

	acr := NewAzureContainerRegistry(cfg)
	acrPullSecret, err := acr.GetPullSecret(ctx)
	if err != nil {
		return fmt.Errorf("error getting pull secret: %w", err)
	}

	acrAuth := types.DockerAuthConfig{Username: "00000000-0000-0000-0000-000000000000", Password: acrPullSecret.RefreshToken}

	for _, repoName := range cfg.Repositories {
		var srcTags, acrTags []string

		baseURL := strings.Split(repoName, "/")[0]
		repoName = strings.Join(strings.Split(repoName, "/")[1:], "/")

		Log().Infow("Syncing repository", "repository", repoName, "baseurl", baseURL)

		if baseURL == "quay.io" {
			srcTags, err = qr.GetTags(ctx, repoName)
			if err != nil {
				return fmt.Errorf("error getting quay tags: %w", err)
			}
			Log().Debugw("Got tags from quay", "tags", srcTags)
		} else {
			oci := NewOCIRegistry(cfg, baseURL)
			srcTags, err = oci.GetTags(ctx, repoName)
			if err != nil {
				return fmt.Errorf("error getting oci tags: %w", err)
			}
			Log().Debugw(fmt.Sprintf("Got tags from %s", baseURL), "repo", repoName, "tags", srcTags)
		}

		exists, err := acr.RepositoryExists(ctx, repoName)
		if err != nil {
			return fmt.Errorf("error getting ACR repository information: %w", err)
		}

		if exists {
			acrTags, err = acr.GetTags(ctx, repoName)
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
			target := fmt.Sprintf("%s/%s:%s", cfg.AcrRegistry, repoName, tagToSync)
			Log().Infow("Copying images", "images", tagToSync, "from", source, "to", target)

			err = Copy(ctx, target, source, &acrAuth, nil)
			if err != nil {
				return fmt.Errorf("error copying image: %w", err)
			}
		}

	}
	return nil
}

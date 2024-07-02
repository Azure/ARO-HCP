package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/containers/image/copy"
	"github.com/containers/image/docker"
	"github.com/containers/image/signature"
	"github.com/containers/image/types"
	"github.com/spf13/viper"
)

type SyncConfig struct {
	Repositories   RepositorySoruce
	NumberOfTags   int
	QuaySecretFile string
	AcrRegistry    string
	TenantId       string
	RequestTimeout int
	AddLatest      bool
}

type QuaySecret struct {
	BearerToken  string
	PullUsername string
	PullPassword string
}

type RepositorySoruce struct {
	Quay []string
}

func NewSyncConfig() *SyncConfig {
	var sc *SyncConfig
	v := viper.GetViper()
	v.SetDefault("numberoftags", 10)
	v.SetDefault("requesttimeout", 10)
	v.SetDefault("addlatest", false)

	if err := v.Unmarshal(&sc); err != nil {
		Log().Fatalw("Error while unmarshalling configuration %s", err.Error())
	}
	Log().Debugw("Using configuration", "config", sc)
	return sc
}

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

func DoSync() {
	cfg := NewSyncConfig()
	Log().Infow("Syncing images", "images", cfg.Repositories, "numberoftags", cfg.NumberOfTags)
	if cfg.NumberOfTags > 50 {
		//Todo implement/test pagination for quay
		Log().Fatalw("Number of tags is too high", "numberoftags", cfg.NumberOfTags)
	}
	ctx := context.Background()

	quaySecret, err := readQuaySecret(cfg.QuaySecretFile)
	if err != nil {
		Log().Fatalw("Error reading secret file", "error", err)
	}

	qr := NewQuayRegistry(cfg, quaySecret.BearerToken)
	acr := NewAzureContainerRegistry(cfg)

	for _, repoName := range cfg.Repositories.Quay {
		var quayTags, acrTags []string

		quayTags, err := qr.GetTags(ctx, repoName)
		if err != nil {
			Log().Fatalw("Error getting tags", "error", err)
		}
		Log().Infow("Got tags from quay", "tags", quayTags)

		exists, err := acr.RepositoryExists(ctx, repoName)
		if err != nil {
			Log().Fatalw("Error getting repository information", "error", err)
		}

		if exists {
			acrTags, err = acr.GetTags(ctx, repoName)
			if err != nil {
				Log().Fatalw("Error getting tags", "error", err)
			}
			Log().Infow("Got tags from acr", "tags", acrTags)
		} else {
			Log().Infow("Repository does not exist", "repository", repoName)
		}

		var tagsToSync []string

		for _, tag := range quayTags {
			found := false
			for _, acrTag := range acrTags {
				if tag == acrTag {
					found = true
					break
				}
			}
			if !found {
				tagsToSync = append(tagsToSync, tag)
			}
		}

		Log().Infow("Images to sync", "images", tagsToSync)

		t, err := acr.GetPullSecret(ctx)
		if err != nil {
			Log().Fatalw("Error getting pull secret", "error", err)
		}

		acrAuth := types.DockerAuthConfig{Username: "00000000-0000-0000-0000-000000000000", Password: t.RefreshToken}
		quayAuth := types.DockerAuthConfig{Username: quaySecret.PullUsername, Password: quaySecret.PullPassword}

		for _, tagToSync := range tagsToSync {
			source := fmt.Sprintf("quay.io/%s:%s", repoName, tagToSync)
			target := fmt.Sprintf("%s/%s:%s", cfg.AcrRegistry, repoName, tagToSync)
			Log().Infow("Copying images", "images", tagsToSync, "from", source, "to", target)

			err = Copy(ctx, target, source, &acrAuth, &quayAuth)
			if err != nil {
				Log().Fatalw("Error copying image", "error", err.Error())

			}
		}

	}

}

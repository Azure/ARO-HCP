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

package main

import (
	"encoding/json"
	defaultlog "log"
	"os"
	"time"

	"github.com/Azure/ARO-HCP/tooling/image-sync/internal"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	syncCmd = &cobra.Command{
		Use:   "image-sync",
		Short: "image-sync",
		Long:  "image-sync",
		RunE: func(cmd *cobra.Command, args []string) error {
			return internal.DoSync(newSyncConfig())
		},
	}
	cfgFile  string
	logLevel string
)

func main() {
	syncCmd.Flags().StringVarP(&cfgFile, "cfgFile", "c", "", "Configuration File")
	syncCmd.Flags().StringVarP(&logLevel, "logLevel", "l", "", "Loglevel (info, debug, error, warn, fatal, panic)")

	cobra.OnInitialize(configureLogging)
	cobra.OnInitialize(initConfig)
	err := syncCmd.Execute()

	if err != nil {
		os.Exit(1)
	}
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
		if err := viper.ReadInConfig(); err != nil {
			Log().Warnw("Error reading config file, using environment variables only", "error", err)
		}
	}
}

// newSyncConfig creates a new SyncConfig from the configuration file
func newSyncConfig() *internal.SyncConfig {
	var sc *internal.SyncConfig
	v := viper.GetViper()
	v.SetDefault("numberoftags", 10)
	v.SetDefault("requesttimeout", 10)
	v.SetDefault("addlatest", false)

	// bind environment variables
	// we can't use vipers native viper.AutomaticEnv() because it only works
	// when also a config file is used
	envVars := map[string]string{
		"NumberOfTags":            "NUMBER_OF_TAGS",
		"RequestTimeout":          "REQUEST_TIMEOUT",
		"AddLatest":               "ADD_LATEST",
		"Repositories":            "REPOSITORIES",
		"AcrTargetRegistry":       "ACR_TARGET_REGISTRY",
		"TenantId":                "TENANT_ID",
		"ManagedIdentityClientID": "MANAGED_IDENTITY_CLIENT_ID",
	}
	for key, env := range envVars {
		if err := v.BindEnv(key, env); err != nil {
			Log().Fatalw("Error while binding environment variable %s: %s", key, err.Error())
		}
	}

	if err := v.Unmarshal(&sc); err != nil {
		Log().Fatalw("Error while unmarshalling configuration %s", err.Error())
	}

	if secretEnv := os.Getenv("SECRETS"); secretEnv != "" {
		type listOfSecrets struct {
			Secrets []internal.Secrets
		}
		var s listOfSecrets
		err := json.Unmarshal([]byte(secretEnv), &s)
		if err != nil {
			Log().Fatal("Error unmarshalling configuration")
		}
		sc.Secrets = append(sc.Secrets, s.Secrets...)
	}

	Log().Debugw("Using configuration", "config", sc)
	return sc
}

func configureLogging() {
	loggerConfig := zap.NewDevelopmentConfig()

	loglevel, err := zap.ParseAtomicLevel(logLevel)
	if err != nil {
		defaultlog.Fatal(err)
	}

	loggerConfig.Level = loglevel

	loggerConfig.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)

	logger, err := loggerConfig.Build()
	zap.ReplaceGlobals(logger)

	if err != nil {
		defaultlog.Fatal(err)
	}
}

func Log() *zap.SugaredLogger {
	return zap.L().Sugar()
}

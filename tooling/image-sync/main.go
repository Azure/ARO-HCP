package main

import (
	defaultlog "log"
	"os"
	"time"

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
			return DoSync()
		},
	}
	cfgFile  string
	logLevel string
)

func Log() *zap.SugaredLogger {
	return zap.L().Sugar()
}

func main() {
	syncCmd.Flags().StringVarP(&cfgFile, "cfgFile", "c", "", "Configuration File")
	syncCmd.Flags().StringVarP(&logLevel, "logLevel", "l", "", "Loglevel (info, debug, error, warn, fatal, panic)")

	cobra.OnInitialize(initConfig)
	cobra.OnInitialize(configureLogging)
	err := syncCmd.Execute()

	if err != nil {
		os.Exit(1)
	}
}

func initConfig() {
	viper.SetConfigFile(cfgFile)
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		Log().Debugw("Using configuration", "config", cfgFile)
	}
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

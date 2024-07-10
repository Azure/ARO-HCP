package main

import (
	defaultlog "log"
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
		Run: func(cmd *cobra.Command, args []string) {
			DoSync()
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
	syncCmd.Execute()
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

	switch logLevel {
	case "info":
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "debug":
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "error":
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	case "warn":
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "fatal":
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.FatalLevel)
	case "panic":
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.PanicLevel)
	default:
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	loggerConfig.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)

	logger, err := loggerConfig.Build()
	zap.ReplaceGlobals(logger)

	if err != nil {
		defaultlog.Fatal(err)
	}
}

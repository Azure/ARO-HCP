package log

import (
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/sirupsen/logrus"
)

func GetLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(ginkgo.GinkgoWriter)
	logger.SetFormatter(&logrus.TextFormatter{
		DisableColors:   true,
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339,
	})

	return logger
}

var Logger *logrus.Logger = GetLogger()

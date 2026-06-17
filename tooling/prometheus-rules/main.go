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
	"flag"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/Azure/ARO-HCP/tooling/prometheus-rules/pkg/prometheusrules"
)

func main() {
	if os.Getenv("DEBUG") == "true" {
		logrus.SetLevel(logrus.DebugLevel)
	}
	var configFilePath string
	var promtoolPath string

	flag.CommandLine.StringVar(&configFilePath, "config-file", "", "Path to configuration")
	flag.CommandLine.StringVar(&promtoolPath, "promtool-path", "promtool", "Path to promtool binary ")
	flag.Parse()

	if err := prometheusrules.Validate(flag.Args(), configFilePath, promtoolPath); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	if err := prometheusrules.GenerateFromConfig(configFilePath, false, promtoolPath); err != nil {
		logrus.WithError(err).Fatal("error running generator")
	}

}

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

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/prometheus-rules/pkg/prometheusrules"
)

func main() {
	if os.Getenv("DEBUG") == "true" {
		logrus.SetLevel(logrus.DebugLevel)
	}
	var configFilePath string
	var promtoolPath string
	var correlationMap bool

	flag.CommandLine.StringVar(&configFilePath, "config-file", "", "Path to configuration")
	flag.CommandLine.StringVar(&promtoolPath, "promtool-path", "promtool", "Path to promtool binary ")
	flag.CommandLine.BoolVar(&correlationMap, "correlation-map", false, "Output a YAML correlation map instead of generating Bicep")
	flag.Parse()

	if correlationMap {
		configs := flag.Args()
		if configFilePath != "" {
			configs = append([]string{configFilePath}, configs...)
		}
		if len(configs) == 0 {
			logrus.Fatal("at least one config file must be provided via --config-file or as arguments")
		}
		entries, err := prometheusrules.GenerateCorrelationMap(configs)
		if err != nil {
			logrus.WithError(err).Fatal("error generating correlation map")
		}
		out, err := yaml.Marshal(entries)
		if err != nil {
			logrus.WithError(err).Fatal("error marshaling correlation map")
		}
		os.Stdout.Write(out)
		return
	}

	if err := prometheusrules.Validate(flag.Args(), configFilePath, promtoolPath); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	if err := prometheusrules.GenerateFromConfig(configFilePath, false, promtoolPath); err != nil {
		logrus.WithError(err).Fatal("error running generator")
	}
}

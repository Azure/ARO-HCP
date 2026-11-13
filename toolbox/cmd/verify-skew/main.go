// Copyright 2026 Microsoft Corporation
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
	"fmt"
	"os"
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v3"
)

var kubernetesVersion = regexp.MustCompile(`^(?:v)?1\.(\d+)(?:\.\d+)?$`)
var kubectlModuleVersion = regexp.MustCompile(`^v0\.(\d+)\.\d+$`)

func parseMinorVersion(version string) (int, error) {
	matches := kubernetesVersion.FindStringSubmatch(version)
	if matches == nil {
		return 0, fmt.Errorf("invalid Kubernetes version %q: expected 1.MINOR[.PATCH]", version)
	}
	minor, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid Kubernetes minor in %q: %w", version, err)
	}
	return minor, nil
}

func checkSkew(configData []byte, kubectlVersion string) error {
	matches := kubectlModuleVersion.FindStringSubmatch(kubectlVersion)
	if matches == nil {
		return fmt.Errorf("invalid kubectl module version %q: expected v0.MINOR.PATCH", kubectlVersion)
	}
	kubectlMinor, err := strconv.Atoi(matches[1])
	if err != nil {
		return fmt.Errorf("invalid kubectl module minor in %q: %w", kubectlVersion, err)
	}

	var config struct {
		Defaults struct {
			Svc struct {
				AKS struct {
					KubernetesVersion string `yaml:"kubernetesVersion"`
				} `yaml:"aks"`
			} `yaml:"svc"`
			Mgmt struct {
				AKS struct {
					KubernetesVersion string `yaml:"kubernetesVersion"`
				} `yaml:"aks"`
			} `yaml:"mgmt"`
		} `yaml:"defaults"`
	}
	if err := yaml.Unmarshal(configData, &config); err != nil {
		return fmt.Errorf("failed to parse AKS configuration: %w", err)
	}

	for _, target := range []struct {
		name    string
		version string
	}{
		{"svc", config.Defaults.Svc.AKS.KubernetesVersion},
		{"mgmt", config.Defaults.Mgmt.AKS.KubernetesVersion},
	} {
		aksMinor, err := parseMinorVersion(target.version)
		if err != nil {
			return fmt.Errorf("%s.aks.kubernetesVersion: %w", target.name, err)
		}
		if kubectlMinor < aksMinor-1 || kubectlMinor > aksMinor+1 {
			return fmt.Errorf("kubectl %s is outside supported +/-1 minor skew from %s AKS %s", kubectlVersion, target.name, target.version)
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: verify-skew <config-file> <kubectl-module-version>")
		os.Exit(2)
	}
	configData, err := os.ReadFile(os.Args[1])
	if err == nil {
		err = checkSkew(configData, os.Args[2])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

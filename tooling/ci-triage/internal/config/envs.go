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

package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GCS and Prow URL constants.
const (
	GCSWebBase = "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results"
	GCSAPI     = "https://storage.googleapis.com/storage/v1/b/test-platform-results/o"
	GCSDirect  = "https://storage.googleapis.com/test-platform-results"
	GCSBucket  = "test-platform-results"

	ProvisionContainer = "aro-hcp-provision-environment"
	GCSPROrgRepo       = "Azure_ARO-HCP"

	MaxMessageChars = 4000
)

// EnvConfig describes a Prow CI environment's job names and artifact paths.
type EnvConfig struct {
	PeriodicJob  string // empty for dev
	PresubmitJob string
	Step         string
	Container    string
}

// Envs maps environment short names to their Prow job configuration.
var Envs = map[string]EnvConfig{
	"dev": {
		PresubmitJob: "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
		Step:         "e2e-parallel",
		Container:    "aro-hcp-test-local",
	},
	"int": {
		PeriodicJob:  "periodic-ci-Azure-ARO-HCP-main-periodic-integration-e2e-parallel",
		PresubmitJob: "pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel",
		Step:         "integration-e2e-parallel",
		Container:    "aro-hcp-test-persistent",
	},
	"stg": {
		PeriodicJob:  "periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel",
		PresubmitJob: "pull-ci-Azure-ARO-HCP-main-stage-e2e-parallel",
		Step:         "stage-e2e-parallel",
		Container:    "aro-hcp-test-persistent",
	},
	"prod": {
		PeriodicJob:  "periodic-ci-Azure-ARO-HCP-main-periodic-prod-e2e-parallel",
		PresubmitJob: "pull-ci-Azure-ARO-HCP-main-prod-e2e-parallel",
		Step:         "prod-e2e-parallel",
		Container:    "aro-hcp-test-persistent",
	},
}

// UnknownEnvError is returned when an invalid environment name is used.
type UnknownEnvError struct {
	Env string
}

func (e *UnknownEnvError) Error() string {
	return fmt.Sprintf("unknown env: %s. Valid: %s", e.Env, strings.Join(EnvNames(), ", "))
}

// EnvNames returns environment names in promotion order.
func EnvNames() []string {
	return []string{"dev", "int", "stg", "prod"}
}

// envURLMarkers maps URL fragments to environment names.
var envURLMarkers = map[string]string{
	"integration-e2e": "int",
	"stage-e2e":       "stg",
	"prod-e2e":        "prod",
}

// DetectEnvFromURL guesses the environment from a Prow job URL.
func DetectEnvFromURL(baseURL string) string {
	for marker, env := range envURLMarkers {
		if strings.Contains(baseURL, marker) {
			return env
		}
	}
	if strings.Contains(baseURL, "e2e-parallel") {
		return "dev"
	}
	return ""
}

// NormalizeBaseURL converts Prow dashboard or relative URLs to canonical gcsweb form.
func NormalizeBaseURL(url string) string {
	if strings.HasPrefix(url, "/") {
		return GCSWebBase + url
	}
	if _, after, ok := strings.Cut(url, "/view/gs/"); ok {
		gcsPath := after
		if qIdx := strings.IndexAny(gcsPath, "?#"); qIdx >= 0 {
			gcsPath = gcsPath[:qIdx]
		}
		gcsPath = strings.TrimRight(gcsPath, "/")
		return "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/" + gcsPath
	}
	u := url
	if qIdx := strings.IndexAny(u, "?#"); qIdx >= 0 {
		u = u[:qIdx]
	}
	return strings.TrimRight(u, "/")
}

// ShortURL strips the GCSWebBase prefix for compact display.
func ShortURL(url string) string {
	if strings.HasPrefix(url, GCSWebBase) {
		return url[len(GCSWebBase):]
	}
	return url
}

var sinceRE = regexp.MustCompile(`^(\d+)([dhw])$`)

// ParseSince resolves relative date shorthand (7d, 24h, 2w) to ISO format.
// Returns empty string if value is empty. Passes through ISO input unchanged.
func ParseSince(value string) (string, error) {
	v := strings.TrimSpace(strings.ToLower(value))
	if v == "" {
		return "", nil
	}
	m := sinceRE.FindStringSubmatch(v)
	if m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := m[2]
		var delta time.Duration
		switch unit {
		case "w":
			delta = time.Duration(n) * 7 * 24 * time.Hour
		case "h":
			delta = time.Duration(n) * time.Hour
		case "d":
			delta = time.Duration(n) * 24 * time.Hour
		}
		dt := time.Now().UTC().Add(-delta)
		if unit == "h" {
			return dt.Format("2006-01-02T15:04:05"), nil
		}
		return dt.Format("2006-01-02"), nil
	}
	// Validate ISO format
	if len(v) >= 10 {
		return value, nil
	}
	return "", fmt.Errorf("invalid --since format: %q (use YYYY-MM-DD, YYYY-MM-DDTHH:MM:SS, or relative like 7d, 24h, 2w)", value)
}

// JobName returns the job name for a given env and job type, or an error.
func JobName(env, jobType string) (string, error) {
	cfg, ok := Envs[env]
	if !ok {
		return "", fmt.Errorf("unknown env: %s. Valid: %s", env, strings.Join(EnvNames(), ", "))
	}
	switch jobType {
	case "periodic":
		if cfg.PeriodicJob == "" {
			return "", fmt.Errorf("no periodic job for env %q", env)
		}
		return cfg.PeriodicJob, nil
	case "presubmit":
		return cfg.PresubmitJob, nil
	default:
		return "", fmt.Errorf("unknown job type: %s", jobType)
	}
}

// JobTypes returns the available job types for an environment.
func JobTypes(env string) []string {
	cfg, ok := Envs[env]
	if !ok {
		return nil
	}
	var types []string
	if cfg.PeriodicJob != "" {
		types = append(types, "periodic")
	}
	types = append(types, "presubmit")
	return types
}

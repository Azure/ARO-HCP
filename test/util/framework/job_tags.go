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

package framework

import (
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
)

const (
	ProwJobIDTag   = "jobID.aro-hcp-ci.redhat.com"
	ProwJobNameTag = "jobName.aro-hcp-ci.redhat.com"

	// Dedicated variables carry flag overrides to run-test subprocesses without
	// changing the CI environment consumed by other tools. Presence, including an
	// empty value, takes precedence over the original environment variable.
	JobNameEnvvar = "ARO_HCP_TEST_JOB_NAME"
	JobIDEnvvar   = "ARO_HCP_TEST_JOB_ID"
)

func JobName() string {
	if value, ok := os.LookupEnv(JobNameEnvvar); ok {
		return value
	}
	return os.Getenv("JOB_NAME")
}

func JobID() string {
	if value, ok := os.LookupEnv(JobIDEnvvar); ok {
		return value
	}
	return os.Getenv("BUILD_ID")
}

// WithJobTags adds the current invocation's attribution without mutating the
// caller's tags. Empty job metadata is omitted; unrelated tags are preserved.
func WithJobTags(tags map[string]*string) map[string]*string {
	tags = maps.Clone(tags)
	for key, value := range map[string]string{ProwJobNameTag: JobName(), ProwJobIDTag: JobID()} {
		for existing := range tags {
			if strings.EqualFold(existing, key) {
				delete(tags, existing)
			}
		}
		if strings.TrimSpace(value) != "" {
			if tags == nil {
				tags = map[string]*string{}
			}
			tags[key] = to.Ptr(value)
		}
	}
	return tags
}

// addDeploymentJobTags supplies the opt-in template parameter and tags nested
// deployments themselves (Bicep module declarations cannot specify tags).
// The template and parameter maps are private to CreateBicepTemplateAndWait.
func addDeploymentJobTags(template, parameters map[string]interface{}) {
	tags := WithJobTags(map[string]*string{})
	if definitions, ok := template["parameters"].(map[string]interface{}); ok {
		if _, acceptsJobTags := definitions["jobTags"]; acceptsJobTags {
			parameters["jobTags"] = map[string]interface{}{"value": tags}
		}
	}
	var visit func(map[string]interface{})
	visit = func(template map[string]interface{}) {
		resources := deploymentTemplateResources(template)
		for _, entry := range resources {
			resource, ok := entry.(map[string]interface{})
			if !ok || resource["type"] != "Microsoft.Resources/deployments" || resource["existing"] == true {
				continue
			}
			resource["tags"] = tags
			properties, _ := resource["properties"].(map[string]interface{})
			nested, _ := properties["template"].(map[string]interface{})
			visit(nested)
		}
	}
	visit(template)
}

func deploymentTemplateResources(template map[string]interface{}) []interface{} {
	switch resources := template["resources"].(type) {
	case []interface{}:
		return resources
	case map[string]interface{}:
		// ARM languageVersion 2.0 uses symbolic resource names as map keys.
		return slices.Collect(maps.Values(resources))
	default:
		return nil
	}
}

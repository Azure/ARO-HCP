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

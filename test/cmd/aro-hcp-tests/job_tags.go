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
	"unicode/utf16"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func bindJobFlags(command *cobra.Command) commandHook {
	jobName, jobID := framework.JobName(), framework.JobID()
	command.Flags().StringVar(&jobName, "job-name", jobName, "Job name used to tag test resources (defaults to JOB_NAME). Empty omits the tag.")
	command.Flags().StringVar(&jobID, "job-id", jobID, "Job ID used to tag test resources (defaults to BUILD_ID). Empty omits the tag.")
	return func(*cobra.Command, []string) error {
		// ARM tag values are limited to 256 UTF-16 code units. Validate before
		// any resources are created, rather than failing partway through setup.
		for _, field := range []struct{ name, value string }{{"job-name", jobName}, {"job-id", jobID}} {
			if len(utf16.Encode([]rune(field.value))) > 256 {
				return fmt.Errorf("--%s must not exceed 256 UTF-16 code units", field.name)
			}
		}
		if err := os.Setenv(framework.JobNameEnvvar, jobName); err != nil {
			return err
		}
		return os.Setenv(framework.JobIDEnvvar, jobID)
	}
}

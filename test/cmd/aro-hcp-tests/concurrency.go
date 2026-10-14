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
	"fmt"
	"os"
	"strconv"
)

// parseSuiteParallelismOverride reads ARO_HCP_SUITE_PARALLELISM and
// returns a non-nil pointer when a valid override is present.
func parseSuiteParallelismOverride() *int {
	v := os.Getenv("ARO_HCP_SUITE_PARALLELISM")
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		fmt.Fprintf(os.Stderr, "WARNING: ARO_HCP_SUITE_PARALLELISM=%q is not a valid positive integer, ignoring override\n", v)
		return nil
	}
	return &n
}

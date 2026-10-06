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

package clusterresources

import (
	"testing"
)

func TestIngressDefaultCertSupported(test *testing.T) {
	for version, expected := range map[string]bool{
		"4.20.42": false, "4.20.43": true, "4.20.44": true,
		"4.21.37": false, "4.21.38": true, "4.21.39": true,
		"4.22.18": false, "4.22.19": true, "4.22.20": true,
		"4.20.42-rc.1": false, "4.20.43-rc.1": true, "4.20.43+build.1": true,
		"4.21.37-rc.1": false, "4.21.38-rc.1": true,
		"4.22.18-rc.1": false, "4.22.19-rc.1": true,
		"4.23.0": true, "4.23.1": true, "4.23.0-rc.1": true,
		"5.0.0": true, "5.0.1": true, "5.0.0-0.nightly": true, "5.0.1-rc.1": true,
		"5.1.0": true, "5.1.1": true, "5.1.0-0.nightly": true, "5.1.0-candidate": true,
		"4.19.99": false, "4.24.0": false, "5.2.0": true, "5.2.0-rc.1": true,
		"6.0.0": true, "6.0.0-rc.1": true, "": false, "invalid": false, "4.23": false,
	} {
		test.Run(version, func(test *testing.T) {
			if actual := ingressDefaultCertSupported(version); actual != expected {
				test.Errorf("version %q supported = %v, want %v", version, actual, expected)
			}
		})
	}
}

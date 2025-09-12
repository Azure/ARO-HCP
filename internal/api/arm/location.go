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

package arm

var azureLocation string

// GetAzureLocation returns the current Azure location.
func GetAzureLocation() string {
	return azureLocation
}

// SetAzureLocation should be called only once on startup.
func SetAzureLocation(location string) {
	azureLocation = location
}

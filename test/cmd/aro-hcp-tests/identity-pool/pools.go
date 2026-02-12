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

package identitypool

type identityPool struct {
	Size                  int
	ResourceGroupBaseName string
	Location              string
	SubscriptionIDHash    string
}

// Pool size calculations are based on the limit of role assignments per subscription (4000)
//
// Rules for pool size calculation:
// * Each HCP created in resourceGroupScope mode consumes 24 role assignments
// * Each HCP created in resourceScope mode consumes 41 role assignments
// * The e2e suite runs all tests using resourceGroupScope except for one test, which uses resourceScope
// * Leave at least 100 role assignments free for other things
//
// max-concurrency = floor(role-assignment-quota - 100 / (((suite-parallelism - 1) * 24) + 41))
// pool-size = max-concurrency * suite-parallelism

var identityPoolMapping = map[string]identityPool{
	"dev": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-dev",
		Location:              "westus3",
		SubscriptionIDHash:    "f5ead0cb5023266042158b287cc43d43e037bc009fb010d3c6efa596b9e18d47",
		// the dev account has a limit of 8000 role assignments after requesting a bump via support.
		Size: 300,
	},
	"int": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-int",
		Location:              "uksouth",
		SubscriptionIDHash:    "25aa33440faa44e53b4e36694bf34b27d67104550f3263bec102c52fafe46191",
		// role-assignment-quota = 4000
		Size: 150,
	},
	"stg": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-stg",
		Location:              "uksouth",
		SubscriptionIDHash:    "ad57837d6720e019ffb0188492ef3bdab91df3762de2908eee18f4e155fdbb85",
		// role-assignment-quota = 4000
		Size: 150,
	},
	"prod": {
		ResourceGroupBaseName: "aro-hcp-test-msi-containers-prod",
		Location:              "uksouth",
		SubscriptionIDHash:    "c71846c9f499272a6baad8fa1ac394f0d2a88fca6d816d0be2dfccd929650588",
		// role-assignment-quota = 4000
		Size: 150,
	},
}

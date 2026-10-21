// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
)

// buildTestProvisionShard returns a minimally-populated ProvisionShard for
// tests in this package. Previously lived in the deleted
// create_cluster_scoped_maestro_readonly_bundles_controller_test.go; kept
// here so the still-relevant delete-orphaned-bundles test can use it.
func buildTestProvisionShard(maestroConsumerName string) *arohcpv1alpha1.ProvisionShard {
	provisionShard, err := arohcpv1alpha1.NewProvisionShard().
		ID("22222222222222222222222222222222").
		MaestroConfig(
			arohcpv1alpha1.NewProvisionShardMaestroConfig().
				ConsumerName(maestroConsumerName).
				RestApiConfig(
					arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().
						Url("https://maestro.example.com:443"),
				).
				GrpcApiConfig(
					arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().
						Url("https://maestro.example.com:444"),
				),
		).
		Build()
	if err != nil {
		panic(err)
	}

	return provisionShard
}

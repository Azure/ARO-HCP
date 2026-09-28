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
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
)

// GetKubeAPIServerVersion returns the kube-apiserver discovery version (/version), bounded by ctx.
//
// Prefer this over discovery.ServerVersion(), which issues its request with context.TODO() and so
// can neither be cancelled nor honour the caller's deadline. Inside a polling verifier that matters:
// a /version request that stalls would run past the verifier's timeout, and past the deadline of
// any phase built on it. Under VerifyHCPCluster, which waits on every verifier it launches, that
// one stalled request holds up the whole batch. Issuing the request through the discovery REST
// client keeps cancellation reaching the HTTP call.
//
// The returned value is unmarshalled from the same payload discovery.ServerVersion() reads, so it is
// directly comparable with values obtained from either.
func GetKubeAPIServerVersion(ctx context.Context, discoveryClient discovery.DiscoveryInterface) (*version.Info, error) {
	restClient := discoveryClient.RESTClient()
	if restClient == nil {
		return nil, fmt.Errorf("discovery client has no REST client to issue a /version request with")
	}

	body, err := restClient.Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return nil, fmt.Errorf("get kube-apiserver /version: %w", err)
	}

	var info version.Info
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse kube-apiserver /version response: %w", err)
	}
	return &info, nil
}

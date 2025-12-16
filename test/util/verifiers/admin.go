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

package verifiers

import (
	"context"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyBreakglassAdminAccessImpl struct{}

var _ HostedClusterVerifier = &verifyBreakglassAdminAccessImpl{}

func VerifyBreakglassAdminAccess() HostedClusterVerifier {
	return &verifyBreakglassAdminAccessImpl{}
}

func (v verifyBreakglassAdminAccessImpl) Name() string {
	return "VerifyBreakglassAdminAccess"
}

// Verify verifies that the provided admin REST config has admin access by creating a SelfSubjectReview
func (v verifyBreakglassAdminAccessImpl) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return err
	}

	response, err := kubeClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	// ensure the SSR identifies the client certificate as having system:masters
	if !sets.New[string](response.Status.UserInfo.Groups...).Has("system:masters") {
		return fmt.Errorf("breakglass admin does not have system:masters group, has groups: %v", response.Status.UserInfo.Groups)
	}

	// TODO: do we want to verify the signer class is customer-break-glass?

	return nil
}

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

package client

import (
	"context"

	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"
)

// CheckAccessV2Client is an interface that allows to interact with Microsoft's Check Access V2 API.
// The Check Access V2 API allows to determine if an Azure identity has a specific set of permissions over a given resource.
type CheckAccessV2Client interface {
	CheckAccess(ctx context.Context, authzReq checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error)
	CreateAuthorizationRequest(resourceId string, actions []string, jwtToken string) (*checkaccessv2.AuthorizationRequest, error)
}

var _ CheckAccessV2Client = (checkaccessv2.RemotePDPClient)(nil)

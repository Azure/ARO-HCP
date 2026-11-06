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

package owners

import (
	"context"
	"fmt"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	graphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
)

type graphDirectory struct {
	client *msgraphsdk.GraphServiceClient
}

// NewGraphDirectory creates a Directory backed by Microsoft Graph.
func NewGraphDirectory(credential azcore.TokenCredential) (Directory, error) {
	client, err := msgraphsdk.NewGraphServiceClientWithCredentials(credential, []string{graph.Scope})
	if err != nil {
		return nil, fmt.Errorf("create Microsoft Graph client: %w", err)
	}
	return &graphDirectory{client: client}, nil
}

func (g *graphDirectory) GroupMembers(ctx context.Context, groupID string) ([]Member, error) {
	builder := g.client.Groups().ByGroupId(groupID).TransitiveMembers()
	var members []Member
	for {
		response, err := builder.Get(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, object := range response.GetValue() {
			// Nested groups are expanded by transitiveMembers; only their
			// leaves can own applications, and devices and contacts cannot.
			switch object.(type) {
			case graphmodels.Userable, graphmodels.ServicePrincipalable:
			default:
				continue
			}
			if object.GetId() == nil {
				continue
			}
			members = append(members, Member{ID: *object.GetId(), DisplayName: displayName(object)})
		}
		next := response.GetOdataNextLink()
		if next == nil || *next == "" {
			return members, nil
		}
		builder = builder.WithUrl(*next)
	}
}

func (g *graphDirectory) Lookup(ctx context.Context, id string) (*Object, error) {
	object, err := g.client.DirectoryObjects().ByDirectoryObjectId(id).Get(ctx, nil)
	switch {
	case err == nil:
		return &Object{DisplayName: displayName(object)}, nil
	case !graph.IsNotFound(err):
		return nil, err
	}

	object, err = g.client.Directory().DeletedItems().ByDirectoryObjectId(id).Get(ctx, nil)
	switch {
	case err == nil:
		return &Object{DisplayName: displayName(object), Deleted: true}, nil
	case graph.IsNotFound(err):
		return nil, nil
	default:
		return nil, err
	}
}

func displayName(object graphmodels.DirectoryObjectable) string {
	switch typed := object.(type) {
	case graphmodels.Userable:
		return ptr.Deref(typed.GetDisplayName(), "")
	case graphmodels.ServicePrincipalable:
		return ptr.Deref(typed.GetDisplayName(), "")
	case graphmodels.Groupable:
		return ptr.Deref(typed.GetDisplayName(), "")
	}
	if name, ok := object.GetAdditionalData()["displayName"].(*string); ok {
		return ptr.Deref(name, "")
	}
	return ""
}

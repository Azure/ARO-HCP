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

package mockidentities

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/owners"
)

const (
	// OwnerIDsEnv carries the comma-separated Entra object IDs that must own
	// every mock identity application and service principal
	// (`entraAppOwnerIds` in config/config-dev-ci.yaml).
	OwnerIDsEnv = "ENTRA_APP_OWNER_IDS"
	// OwnersGroupIDEnv carries the object ID of the Entra group those owners
	// mirror (`entraAppOwnersGroupId`), used to reject a drifted list before
	// any write.
	OwnersGroupIDEnv = "ENTRA_APP_OWNERS_GROUP_ID"

	directoryObjectsURL = "https://graph.microsoft.com/v1.0/directoryObjects/"
)

// ownerSettings is the authoritative owner list for mock identity objects.
type ownerSettings struct {
	IDs     []string
	GroupID string
}

func ownerSettingsFromEnv() (ownerSettings, error) {
	ids, err := owners.ParseIDs(os.Getenv(OwnerIDsEnv))
	if err != nil {
		return ownerSettings{}, fmt.Errorf("%s: %w (run through `make -C test apply-pool-assets`, which sets it from config/config-dev-ci.yaml)", OwnerIDsEnv, err)
	}
	groupID := strings.TrimSpace(os.Getenv(OwnersGroupIDEnv))
	groups, err := owners.ParseIDs(groupID)
	if err != nil || len(groups) != 1 {
		return ownerSettings{}, fmt.Errorf("%s must be one valid object ID", OwnersGroupIDEnv)
	}
	return ownerSettings{IDs: ids, GroupID: groups[0]}, nil
}

// ownerRelations reads and edits the owner relationship of one Entra object.
type ownerRelations struct {
	list   func(context.Context) ([]string, error)
	add    func(context.Context, string) error
	remove func(context.Context, string) error
}

type ownerPlan struct {
	name           string
	relations      ownerRelations
	missing, extra []string
}

func reconcileOwnerSets(ctx context.Context, plans []ownerPlan, desired []string, apply bool) error {
	desired, err := owners.ParseIDs(strings.Join(desired, ","))
	if err != nil {
		return err
	}
	drift := false
	for index := range plans {
		plan := &plans[index]
		plan.missing, plan.extra = nil, nil
		current, err := plan.relations.list(ctx)
		if err != nil {
			return fmt.Errorf("list owners of %s: %w", plan.name, err)
		}
		for _, id := range desired {
			if !slices.Contains(current, id) {
				plan.missing = append(plan.missing, id)
			}
		}
		for _, id := range current {
			if !slices.Contains(desired, id) {
				plan.extra = append(plan.extra, id)
			}
		}
		drift = drift || len(plan.missing)+len(plan.extra) > 0
	}
	if !drift {
		return nil
	}
	if !apply {
		return fmt.Errorf("owners differ from entraAppOwnerIds")
	}
	for _, plan := range plans {
		for _, id := range plan.missing {
			if err := plan.relations.add(ctx, id); err != nil {
				return fmt.Errorf("add owner %s to %s: %w", id, plan.name, err)
			}
		}
	}
	for _, plan := range plans {
		current, err := plan.relations.list(ctx)
		if err != nil {
			return err
		}
		for _, id := range desired {
			if !slices.Contains(current, id) {
				return fmt.Errorf("owner additions to %s not yet visible; retry apply", plan.name)
			}
		}
	}
	for _, plan := range plans {
		for _, id := range plan.extra {
			if err := plan.relations.remove(ctx, id); err != nil {
				return fmt.Errorf("remove owner %s from %s: %w; retry with an authorized owner or application administrator", id, plan.name, err)
			}
		}
	}
	return reconcileOwnerSets(ctx, plans, desired, false)
}

func (store *azureStore) Owners(ctx context.Context, name string, principal leaseenv.MockPrincipal, desired []string, apply bool) error {
	if _, err := owners.ParseIDs(strings.Join(desired, ",")); err != nil {
		return err
	}
	application, err := store.graph.ApplicationsWithUniqueName(&name).Get(ctx, nil)
	if err != nil {
		return err
	}
	if application == nil || ptr.Deref(application.GetDisplayName(), "") != name || ptr.Deref(application.GetAppId(), "") != principal.ClientID || principal.CertificateName != name {
		return fmt.Errorf("application identity for %q changed before owner reconciliation", name)
	}
	applicationID := ptr.Deref(application.GetId(), "")
	if _, err := owners.ParseIDs(strings.Join([]string{applicationID, principal.ClientID, principal.PrincipalID}, ",")); err != nil || applicationID == "" || principal.ClientID == "" || principal.PrincipalID == "" {
		return fmt.Errorf("invalid identity IDs for %q", name)
	}
	actual, err := store.graph.ServicePrincipalsWithAppId(&principal.ClientID).Get(ctx, nil)
	if err != nil {
		return err
	}
	if actual == nil || ptr.Deref(actual.GetId(), "") != principal.PrincipalID || ptr.Deref(actual.GetAppId(), "") != principal.ClientID || !ptr.Deref(actual.GetAccountEnabled(), false) {
		return fmt.Errorf("service principal identity for %q changed before owner reconciliation", name)
	}
	return reconcileOwnerSets(ctx, []ownerPlan{{name: "application " + name, relations: store.applicationOwners(applicationID)}, {name: "service principal " + name, relations: store.servicePrincipalOwners(principal.PrincipalID)}}, desired, apply)
}

func (store *azureStore) applicationOwners(id string) ownerRelations {
	builder := store.graph.Applications().ByApplicationId(id).Owners()
	return ownerRelations{
		list: func(ctx context.Context) ([]string, error) {
			first, err := builder.Get(ctx, nil)
			if err != nil {
				return nil, err
			}
			objects, err := collectOwnerObjects(ctx, "/v1.0/applications/"+id+"/owners", first, func(ctx context.Context, link string) (models.DirectoryObjectCollectionResponseable, error) {
				return builder.WithUrl(link).Get(ctx, nil)
			})
			if err != nil {
				return nil, err
			}
			return objectIDs(objects)
		},
		add: func(ctx context.Context, owner string) error {
			return builder.Ref().Post(ctx, ownerReference(owner), nil)
		},
		remove: func(ctx context.Context, owner string) error {
			return builder.ByDirectoryObjectId(owner).Ref().Delete(ctx, nil)
		},
	}
}

func (store *azureStore) servicePrincipalOwners(id string) ownerRelations {
	builder := store.graph.ServicePrincipals().ByServicePrincipalId(id).Owners()
	return ownerRelations{
		list: func(ctx context.Context) ([]string, error) {
			first, err := builder.Get(ctx, nil)
			if err != nil {
				return nil, err
			}
			objects, err := collectOwnerObjects(ctx, "/v1.0/servicePrincipals/"+id+"/owners", first, func(ctx context.Context, link string) (models.DirectoryObjectCollectionResponseable, error) {
				return builder.WithUrl(link).Get(ctx, nil)
			})
			if err != nil {
				return nil, err
			}
			return objectIDs(objects)
		},
		add: func(ctx context.Context, owner string) error {
			return builder.Ref().Post(ctx, ownerReference(owner), nil)
		},
		remove: func(ctx context.Context, owner string) error {
			return builder.ByDirectoryObjectId(owner).Ref().Delete(ctx, nil)
		},
	}
}

func ownerReference(id string) models.ReferenceCreateable {
	reference := models.NewReferenceCreate()
	reference.SetOdataId(ptr.To(directoryObjectsURL + id))
	return reference
}

func objectIDs(objects []models.DirectoryObjectable) ([]string, error) {
	ids := make([]string, 0, len(objects))
	for _, object := range objects {
		if object == nil || ptr.Deref(object.GetId(), "") == "" {
			return nil, fmt.Errorf("owner enumeration contains an object without an ID")
		}
		id, err := uuid.Parse(*object.GetId())
		if err != nil {
			return nil, fmt.Errorf("invalid owner object ID: %w", err)
		}
		ids = append(ids, id.String())
	}
	if len(ids) == 0 {
		return ids, nil
	}
	return owners.ParseIDs(strings.Join(ids, ","))
}

func collectOwnerObjects(ctx context.Context, collectionPath string, page models.DirectoryObjectCollectionResponseable, next func(context.Context, string) (models.DirectoryObjectCollectionResponseable, error)) ([]models.DirectoryObjectable, error) {
	var result []models.DirectoryObjectable
	seen := map[string]bool{}
	for {
		if page == nil || page.GetValue() == nil {
			return nil, fmt.Errorf("incomplete directory enumeration")
		}
		if _, err := objectIDs(page.GetValue()); err != nil {
			return nil, err
		}
		result = append(result, page.GetValue()...)
		link := page.GetOdataNextLink()
		if link == nil {
			if _, err := objectIDs(result); err != nil {
				return nil, err
			}
			return result, nil
		}
		parsed, err := url.Parse(*link)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "graph.microsoft.com" || parsed.Path != collectionPath || parsed.User != nil || seen[*link] || len(seen) >= 1000 {
			return nil, fmt.Errorf("invalid or repeated directory continuation link")
		}
		seen[*link] = true
		page, err = next(ctx, *link)
		if err != nil {
			return nil, err
		}
	}
}

type strictOwnerDirectory struct {
	owners.Directory
	store *azureStore
}

func (directory strictOwnerDirectory) GroupMembers(ctx context.Context, groupID string) ([]owners.Member, error) {
	builder := directory.store.graph.Groups().ByGroupId(groupID).TransitiveMembers()
	first, err := builder.Get(ctx, nil)
	if err != nil {
		return nil, err
	}
	objects, err := collectOwnerObjects(ctx, "/v1.0/groups/"+groupID+"/transitiveMembers", first, func(ctx context.Context, link string) (models.DirectoryObjectCollectionResponseable, error) {
		return builder.WithUrl(link).Get(ctx, nil)
	})
	if err != nil {
		return nil, err
	}
	var members []owners.Member
	for _, object := range objects {
		switch object.(type) {
		case models.Userable, models.ServicePrincipalable:
			members = append(members, owners.Member{ID: *object.GetId()})
		case models.Groupable, models.Deviceable, models.OrgContactable:
		default:
			return nil, fmt.Errorf("group enumeration contains an unknown object type")
		}
	}
	return members, nil
}

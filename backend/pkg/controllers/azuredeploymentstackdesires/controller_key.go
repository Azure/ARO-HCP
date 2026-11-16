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

package azuredeploymentstackdesires

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Key identifies a single DeploymentStackDesire by its parent resource ID and
// name. ParentResourceID is parent-agnostic so the controller can deal with DeploymentStackDesires of any scope.
type Key struct {
	ParentResourceID string
	Name             string
}

// CRUD returns the right per-scope CRUD for this key's parent.
func (k Key) CRUD(client azureresourcescosmosstorage.AzureResourcesDeploymentStackDesireCRUD) (cosmosstorageutils.ResourceCRUD[azureresourcesapi.DeploymentStackDesire, *azureresourcesapi.DeploymentStackDesire], error) {
	id, err := azcorearm.ParseResourceID(k.ParentResourceID)
	if err != nil {
		return nil, err
	}
	parent, err := azureresourcescosmosstorage.ParseDeploymentStackDesireScope(id)
	if err != nil {
		return nil, err
	}
	return client.DeploymentStackDesiresFor(parent)
}

// GetResourceID returns the desire's full resource ID.
func (k Key) GetResourceID() *azcorearm.ResourceID {
	s := strings.ToLower(path.Join(k.ParentResourceID, azureresourcesapi.DeploymentStackDesireResourceTypeName, k.Name))
	return metadataapi.Must(azcorearm.ParseResourceID(s))
}

func (k Key) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(k.GetResourceID())...)
}

// KeyFromResourceID parses a Key out of a DeploymentStackDesire's resource ID.
// The desire is the leaf; everything above it is the parent. Mirrors
// kube-applier's keys.ApplyDesireKeyFromResourceID.
func KeyFromResourceID(id *azcorearm.ResourceID) (Key, error) {
	if id == nil {
		return Key{}, errors.New("resource ID is nil")
	}
	if id.Parent == nil {
		return Key{}, fmt.Errorf("desire %q has no parent in its resource ID", id.String())
	}
	return Key{ParentResourceID: strings.ToLower(id.Parent.String()), Name: id.Name}, nil
}

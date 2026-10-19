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

package azureutil

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// ParsePrincipalID requires a hyphenated UUID without surrounding whitespace.
func ParsePrincipalID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, err
	}
	if !strings.EqualFold(value, id.String()) {
		return uuid.Nil, errors.New("principal ID must be a hyphenated UUID without surrounding whitespace")
	}
	return id, nil
}

// NormalizedNameSet lowercases names for case-insensitive identity matching.
func NormalizedNameSet(names []string) sets.Set[string] {
	normalized := sets.New[string]()
	for _, name := range names {
		normalized.Insert(strings.ToLower(name))
	}
	return normalized
}

// RequireIdentities fails closed when any expected identity is missing from actual.
// actual maps lowercase names to the Azure-reported spelling.
func RequireIdentities(resourceGroup string, expected sets.Set[string], actual map[string]string) error {
	missing := sets.List(expected.Difference(sets.KeySet(actual)))
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("identity inventory drift in resource group %q: missing=%v", resourceGroup, missing)
}

// IsNotFound reports whether err is an Azure SDK 404.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var responseError *azcore.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound
}

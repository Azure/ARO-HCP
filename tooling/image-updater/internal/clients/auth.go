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

package clients

import (
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// GetRemoteOptions returns remote options with Docker credentials if useAuth is true
// It uses the default Docker config file (~/.docker/config.json) for authentication
func GetRemoteOptions(useAuth bool) []remote.Option {
	if !useAuth {
		return nil
	}

	// Use the default keychain which reads from ~/.docker/config.json
	// This automatically handles authentication for registries the user has logged into
	return []remote.Option{
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
	}
}

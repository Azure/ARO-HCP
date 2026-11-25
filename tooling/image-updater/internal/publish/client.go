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

package publish

import (
	"context"
	"fmt"
	"net/http"

	gh "github.com/google/go-github/v69/github"

	githublib "github.com/Azure/ARO-HCP/tooling/github"
)

// TokenSource returns the credential used to push and publish.
type TokenSource func(ctx context.Context) (string, error)

// newPublisher binds the shared client to the installation token and actor.
func newPublisher(repository, token, actor string) (Publisher, error) {
	api := gh.NewClient(&http.Client{Transport: &tokenTransport{token: token}})
	client, err := githublib.NewClient(repository, api)
	if err != nil {
		return nil, fmt.Errorf("build the publication client: %w", err)
	}
	bound, err := client.WithActor(actor)
	if err != nil {
		return nil, fmt.Errorf("bind the publication actor: %w", err)
	}
	return bound, nil
}

// tokenTransport adds the installation token to every request.
type tokenTransport struct {
	token string
}

// RoundTrip attaches the bearer token and delegates to the default transport.
func (t *tokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	clone.Header.Set("Accept", "application/vnd.github+json")
	return http.DefaultTransport.RoundTrip(clone)
}

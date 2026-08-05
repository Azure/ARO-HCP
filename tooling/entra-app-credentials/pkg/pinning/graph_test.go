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

package pinning

import (
	"context"
	"crypto/sha1" //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	kiotaabstractions "github.com/microsoft/kiota-abstractions-go"
	graphmodels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeGraphAPI struct {
	listResults [][]graphmodels.Applicationable
	listErr     error
	replaceID   string
	replaceKey  []byte
	replaceErrs []error
	getResult   graphmodels.Applicationable
	getErrs     []error
}

func (f *fakeGraphAPI) ListApplications(context.Context, string) ([]graphmodels.Applicationable, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	result := f.listResults[0]
	f.listResults = f.listResults[1:]
	return result, nil
}

func (f *fakeGraphAPI) ReplaceKeyCredentials(_ context.Context, applicationID, _ string, certificate []byte) error {
	if len(f.replaceErrs) > 0 {
		err := f.replaceErrs[0]
		f.replaceErrs = f.replaceErrs[1:]
		if err != nil {
			return err
		}
	}
	f.replaceID = applicationID
	f.replaceKey = certificate
	return nil
}

func (f *fakeGraphAPI) GetApplication(context.Context, string) (graphmodels.Applicationable, error) {
	if len(f.getErrs) > 0 {
		err := f.getErrs[0]
		f.getErrs = f.getErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	return f.getResult, nil
}

func TestGraphClientFindApplication(t *testing.T) {
	api := &fakeGraphAPI{listResults: [][]graphmodels.Applicationable{{newGraphApplication("object-id", "client-id", "app")}}}
	client := newGraphClient(api)

	application, err := client.FindApplication(context.Background(), "app")
	require.NoError(t, err)
	assert.Equal(t, "object-id", application.ID)
	assert.Equal(t, "client-id", application.AppID)
}

func TestGraphClientRejectsDuplicateDisplayNames(t *testing.T) {
	api := &fakeGraphAPI{listResults: [][]graphmodels.Applicationable{{
		newGraphApplication("one", "", "duplicate"),
		newGraphApplication("two", "", "duplicate"),
	}}}
	client := newGraphClient(api)

	_, err := client.FindApplication(context.Background(), "duplicate")
	require.ErrorContains(t, err, "matched 2 applications")
}

func TestGraphClientRetriesApplicationPropagation(t *testing.T) {
	oldInterval := retryInterval
	retryInterval = time.Millisecond
	t.Cleanup(func() { retryInterval = oldInterval })

	api := &fakeGraphAPI{listResults: [][]graphmodels.Applicationable{
		nil,
		{newGraphApplication("object-id", "", "app")},
	}}
	client := newGraphClient(api)

	_, err := client.FindApplication(context.Background(), "app")
	require.NoError(t, err)
	assert.Empty(t, api.listResults)
}

func TestGraphClientReturnsSDKError(t *testing.T) {
	client := newGraphClient(&fakeGraphAPI{listErr: errors.New("forbidden")})

	_, err := client.FindApplication(context.Background(), "app")
	require.ErrorContains(t, err, "forbidden")
}

func TestGraphClientDelegatesReplacement(t *testing.T) {
	api := &fakeGraphAPI{}
	client := newGraphClient(api)

	err := client.ReplaceKeyCredentials(context.Background(), "object-id", "cert", []byte("certificate"))
	require.NoError(t, err)
	assert.Equal(t, "object-id", api.replaceID)
	assert.Equal(t, []byte("certificate"), api.replaceKey)
}

func TestGraphClientRetriesNotFound(t *testing.T) {
	oldInterval := retryInterval
	retryInterval = time.Millisecond
	t.Cleanup(func() { retryInterval = oldInterval })

	notFound := kiotaabstractions.NewApiError()
	notFound.SetStatusCode(http.StatusNotFound)

	api := &fakeGraphAPI{
		replaceErrs: []error{notFound, nil},
		getErrs:     []error{notFound, nil},
		getResult:   newGraphApplication("object-id", "client-id", "app"),
	}
	client := newGraphClient(api)

	err := client.ReplaceKeyCredentials(context.Background(), "object-id", "cert", []byte("certificate"))
	require.NoError(t, err)
	assert.Empty(t, api.replaceErrs)

	application, err := client.GetApplication(context.Background(), "object-id")
	require.NoError(t, err)
	assert.Equal(t, "object-id", application.ID)
	assert.Empty(t, api.getErrs)
}

func TestClassifyGraphErrorOnlyRetriesNotFoundAndNetworkErrors(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusBadRequest,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		apiError := kiotaabstractions.NewApiError()
		apiError.SetStatusCode(statusCode)
		assert.Same(t, apiError, classifyGraphError(apiError))
	}

	notFound := kiotaabstractions.NewApiError()
	notFound.SetStatusCode(http.StatusNotFound)
	var transient *transientError
	require.ErrorAs(t, classifyGraphError(notFound), &transient)
	assert.Same(t, notFound, transient.err)

	networkError := &net.DNSError{Err: "temporary failure", IsTemporary: true}
	require.ErrorAs(t, classifyGraphError(networkError), &transient)
	assert.Same(t, networkError, transient.err)
}

func TestNormalizeCustomKeyIdentifier(t *testing.T) {
	thumbprint := sha1.Sum([]byte("certificate")) //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	hexValue := []byte(hex.EncodeToString(thumbprint[:]))
	base64DecodedHex, err := base64.StdEncoding.DecodeString(string(hexValue))
	require.NoError(t, err)

	assert.Equal(t, thumbprint[:], normalizeCustomKeyIdentifier(thumbprint[:]))
	assert.Equal(t, thumbprint[:], normalizeCustomKeyIdentifier(hexValue))
	assert.Equal(t, thumbprint[:], normalizeCustomKeyIdentifier(base64DecodedHex))
}

func TestConvertApplicationRejectsMissingID(t *testing.T) {
	_, err := convertApplication(graphmodels.NewApplication())
	require.ErrorContains(t, err, "has no ID")
}

func newGraphApplication(id, appID, displayName string) graphmodels.Applicationable {
	application := graphmodels.NewApplication()
	application.SetId(&id)
	application.SetAppId(&appID)
	application.SetDisplayName(&displayName)
	return application
}

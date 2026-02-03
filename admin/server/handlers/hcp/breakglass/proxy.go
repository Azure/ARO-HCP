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

package breakglass

import (
	"fmt"
	"net/http"
	"net/url"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/proxy"

	"github.com/Azure/ARO-HCP/internal/utils"
	sessiongatelisterv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/listers/sessiongate/v1alpha1"
)

// HCPBreakglassSessionKASProxyHandler proxies authenticated requests to the HCP's kube-apiserver.
//
// Authorization Model:
// This handler does NOT perform authorization checks in code. Authorization is enforced externally:
//
//  1. MISE validates the Azure access token
//  2. Istio AuthorizationPolicies (managed by sessiongate controller) enforce session ownership:
//     - The policy matches the session's owner claims (upn/appid) against the authenticated principal
//     - Requests from non-owners are rejected at the Istio layer before reaching this handler
//
// This handler only needs to:
//   - Look up the session to get the backend endpoint
//   - Proxy the request to the HCP's kube-apiserver
//
// See package documentation for full security model.
type HCPBreakglassSessionKASProxyHandler struct {
	sessionLister sessiongatelisterv1alpha1.SessionNamespaceLister
}

func NewHCPBreakglassSessionKASProxyHandler(sessionLister sessiongatelisterv1alpha1.SessionNamespaceLister) *HCPBreakglassSessionKASProxyHandler {
	return &HCPBreakglassSessionKASProxyHandler{
		sessionLister: sessionLister,
	}
}

func (h *HCPBreakglassSessionKASProxyHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	logger := utils.LoggerFromContext(request.Context())

	sessionName := request.PathValue("sessionName")
	if sessionName == "" {
		http.Error(writer, "session parameter is required", http.StatusBadRequest)
		return
	}

	session, err := h.sessionLister.Get(sessionName)
	if err != nil && !apierrors.IsNotFound(err) {
		if apierrors.IsNotFound(err) {
			// Session not yet visible in cache - treat as pending and retry
			http.Error(writer, "Session is being created", http.StatusAccepted)
			return
		}
		logger.Error(err, "failed to get session", "sessionName", sessionName)
		http.Error(writer, fmt.Sprintf("failed to get session: %v", err), http.StatusInternalServerError)
		return
	}

	backendURL, err := url.Parse(session.Status.Endpoint)
	if err != nil {
		logger.Error(err, "failed to parse session endpoint", "sessionName", sessionName, "endpoint", session.Status.Endpoint)
		http.Error(writer, fmt.Sprintf("failed to parse session endpoint: %v", err), http.StatusInternalServerError)
		return
	}

	backendURL.Path = backendURL.Path + "/" + request.PathValue("path")
	backendURL.RawQuery = request.URL.RawQuery

	logger.V(1).Info("proxying request to sessiongate", "sessionName", sessionName, "backendURL", backendURL.String())

	proxyHandler := proxy.NewUpgradeAwareHandler(backendURL, nil, true, false, nil)
	proxyHandler.ServeHTTP(writer, request)
}

func protocolAndHostFromRequest(request *http.Request) (string, string) {
	scheme := request.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		// fallback if no header is set
		scheme = "http"
		if request.TLS != nil {
			scheme = "https"
		}
	}
	host := request.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = request.Host
	}
	return scheme, host
}

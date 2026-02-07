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
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clientcmd "k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/ARO-HCP/internal/utils"
	sessiongateapiv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	sessiongatelisterv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/listers/sessiongate/v1alpha1"
)

// HCPBreakglassSessionKubeconfigHandler handles requests to retrieve kubeconfig for a session.
// This endpoint is accessed exclusively via Geneva Actions. See package documentation for security model.
type HCPBreakglassSessionKubeconfigHandler struct {
	sessionLister                     sessiongatelisterv1alpha1.SessionNamespaceLister
	breakglassSessionProxyPathPattern string
}

func NewHCPBreakglassSessionKubeconfigHandler(sessionLister sessiongatelisterv1alpha1.SessionNamespaceLister, breakglassSessionProxyPathPattern string) *HCPBreakglassSessionKubeconfigHandler {
	return &HCPBreakglassSessionKubeconfigHandler{
		sessionLister:                     sessionLister,
		breakglassSessionProxyPathPattern: breakglassSessionProxyPathPattern,
	}
}

func (h *HCPBreakglassSessionKubeconfigHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	logger := utils.LoggerFromContext(request.Context())

	sessionName := request.PathValue("sessionName")
	if sessionName == "" {
		http.Error(writer, "session parameter is required", http.StatusBadRequest)
		return
	}

	session, err := h.sessionLister.Get(sessionName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Session not yet visible in cache - treat as pending and retry
			h.writeRetryResponse(writer, logger, sessionName, "Session is being created")
			return
		}
		logger.Error(err, "failed to get session from lister", "sessionName", sessionName)
		http.Error(writer, "failed to retrieve session", http.StatusInternalServerError)
		return
	}

	if !session.IsReady() {
		message := "Session is not ready"
		if readyCondition := session.GetCondition(sessiongateapiv1alpha1.SessionConditionTypeReady); readyCondition != nil {
			message = readyCondition.Message
		}
		h.writeRetryResponse(writer, logger, sessionName, message)
		return
	}

	scheme, host := protocolAndHostFromRequest(request)
	endpoint := url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   fmt.Sprintf(h.breakglassSessionProxyPathPattern, sessionName),
	}
	kubeconfig, err := session.GetKubeconfig(endpoint.String())
	if err != nil {
		logger.Error(err, "failed to get kubeconfig from session", "sessionName", sessionName)
		http.Error(writer, "failed to generate kubeconfig", http.StatusInternalServerError)
		return
	}
	kubeconfigBytes, err := clientcmd.Write(kubeconfig)
	if err != nil {
		logger.Error(err, "failed to serialize kubeconfig", "sessionName", sessionName)
		http.Error(writer, "failed to generate kubeconfig", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/yaml")
	writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"kubeconfig-%s.yaml\"", sessionName))
	writer.WriteHeader(http.StatusOK)
	_, err = writer.Write(kubeconfigBytes)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to write response for session %s", sessionName))
	}
}

func (h *HCPBreakglassSessionKubeconfigHandler) writeRetryResponse(writer http.ResponseWriter, logger logr.Logger, sessionName, message string) {
	writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	writer.Header().Set("Retry-After", "5")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusAccepted)
	data := map[string]string{
		"status": message,
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		logger.Error(err, "failed to marshal retry response", "sessionName", sessionName)
		http.Error(writer, "internal error", http.StatusInternalServerError)
		return
	}
	_, err = writer.Write(jsonBytes)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to write response for session %s", sessionName))
	}
}

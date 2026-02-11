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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/ARO-HCP/internal/utils"
	sessiongateapiv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	sessiongateclientv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned/typed/sessiongate/v1alpha1"
	sessiongatelisterv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/listers/sessiongate/v1alpha1"
)

const (
	// sessionNotReadyRetryAfterSeconds is the Retry-After value (in seconds) sent to clients
	// when a session is not yet ready.
	sessionNotReadyRetryAfterSeconds = 5
)

// HCPBreakglassSessionKubeconfigHandler handles requests to retrieve kubeconfig for a session.
// This endpoint is accessed exclusively via Geneva Actions. See package documentation for security model.
type HCPBreakglassSessionKubeconfigHandler struct {
	sessionLister sessiongatelisterv1alpha1.SessionNamespaceLister
	sessionClient sessiongateclientv1alpha1.SessionInterface
}

func NewHCPBreakglassSessionKubeconfigHandler(sessionLister sessiongatelisterv1alpha1.SessionNamespaceLister, sessionClient sessiongateclientv1alpha1.SessionInterface) *HCPBreakglassSessionKubeconfigHandler {
	return &HCPBreakglassSessionKubeconfigHandler{
		sessionLister: sessionLister,
		sessionClient: sessionClient,
	}
}

func (h *HCPBreakglassSessionKubeconfigHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	logger := utils.LoggerFromContext(request.Context())

	sessionName := request.PathValue("sessionName")
	if sessionName == "" {
		http.Error(writer, "session parameter is required", http.StatusBadRequest)
		return
	}
	logger = logger.WithValues("sessionName", sessionName)

	// Try to get session from lister first (cached)
	session, err := h.getSession(request.Context(), sessionName)
	if err != nil {
		logger.Error(err, "failed to get session")
		http.Error(writer, "session not found", http.StatusNotFound)
		return
	}

	if !session.IsReady() {
		details := GetSessionNotReadyDetails(session)
		h.writeRetryResponse(writer, logger, details, sessionNotReadyRetryAfterSeconds)
		return
	}

	kubeconfig, err := session.GetKubeconfig(session.Status.Endpoint)
	if err != nil {
		logger.Error(err, "failed to get kubeconfig from session")
		http.Error(writer, "failed to generate kubeconfig", http.StatusInternalServerError)
		return
	}
	if session.Status.ExpiresAt == nil {
		logger.Error(nil, "session is ready but expiresAt is not set")
		http.Error(writer, "unable to determine session expiration", http.StatusInternalServerError)
		return
	}
	h.writeKubeconfigResponse(writer, logger, sessionName, kubeconfig, session.Status.ExpiresAt.Time)
}

func (h *HCPBreakglassSessionKubeconfigHandler) getSession(ctx context.Context, sessionName string) (*sessiongateapiv1alpha1.Session, error) {
	session, err := h.sessionLister.Get(sessionName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return h.sessionClient.Get(ctx, sessionName, metav1.GetOptions{})
		}
		return nil, fmt.Errorf("failed to get session from lister: %w", err)
	}
	return session, nil
}

func (h *HCPBreakglassSessionKubeconfigHandler) writeRetryResponse(writer http.ResponseWriter, logger logr.Logger, sessionStatus map[string]any, retryAfter int) {
	writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	writer.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusAccepted)

	jsonBytes, _ := json.Marshal(sessionStatus)

	_, err := writer.Write(jsonBytes)
	if err != nil {
		logger.Error(err, "failed to write retry response")
		http.Error(writer, "failed to generate retry response", http.StatusInternalServerError)
		return
	}
}

func (h *HCPBreakglassSessionKubeconfigHandler) writeKubeconfigResponse(writer http.ResponseWriter, logger logr.Logger, sessionName string, kubeconfig api.Config, expiresAt time.Time) {
	kubeconfigBytes, err := clientcmd.Write(kubeconfig)
	if err != nil {
		logger.Error(err, "failed to serialize kubeconfig")
		http.Error(writer, "failed to generate kubeconfig", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Expires", expiresAt.Format(time.RFC3339))
	writer.Header().Set("Content-Type", "application/yaml")
	writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"kubeconfig-%s.yaml\"", sessionName))
	writer.WriteHeader(http.StatusOK)
	_, err = writer.Write(kubeconfigBytes)
	if err != nil {
		logger.Error(err, "failed to write kubeconfig")
		http.Error(writer, "failed to generate kubeconfig", http.StatusInternalServerError)
		return
	}
}

func GetSessionNotReadyDetails(session *sessiongateapiv1alpha1.Session) map[string]any {
	details := map[string]any{
		"status": "Session is not ready",
	}

	if readyCondition := session.GetCondition(sessiongateapiv1alpha1.SessionConditionTypeReady); readyCondition != nil {
		details["status"] = readyCondition.Message
	}

	// Check all non-Ready conditions and include details if not True
	conditionsToCheck := []sessiongateapiv1alpha1.SessionConditionType{
		sessiongateapiv1alpha1.SessionConditionTypeHostedControlPlaneAvailable,
		sessiongateapiv1alpha1.SessionConditionTypeCredentialsAvailable,
		sessiongateapiv1alpha1.SessionConditionTypeNetworkPathAvailable,
	}

	for _, conditionType := range conditionsToCheck {
		if condition := session.GetCondition(conditionType); condition != nil && condition.Status != metav1.ConditionTrue {
			details[string(conditionType)] = map[string]string{
				"status":  string(condition.Status),
				"reason":  condition.Reason,
				"message": condition.Message,
			}
		}
	}

	return details
}

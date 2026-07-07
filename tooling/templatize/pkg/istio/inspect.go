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

package istio

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func logMeshState(ctx context.Context, client kubernetes.Interface, logger logr.Logger) {
	namespaces, err := GetMeshNamespaces(ctx, client)
	if err != nil {
		logger.Error(err, "failed to query mesh namespaces for inspection")
		return
	}

	cpStatuses, err := GetControlPlaneStatus(ctx, client)
	if err != nil {
		logger.Error(err, "failed to query control planes for inspection")
		return
	}

	gwStatuses, err := GetIngressGatewayStatus(ctx, client)
	if err != nil {
		logger.Error(err, "failed to query ingress gateways for inspection")
		return
	}

	webhooks, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Error(err, "failed to query webhooks for inspection")
		return
	}
	istioWebhooks := 0
	for _, wh := range webhooks.Items {
		if strings.Contains(wh.Name, "istio") {
			istioWebhooks++
		}
	}

	cms, err := client.CoreV1().ConfigMaps(istioSystemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "istio.io/rev",
	})
	if err != nil {
		logger.Error(err, "failed to query configmaps for inspection")
		return
	}

	var cpInfo []string
	for _, cp := range cpStatuses {
		cpInfo = append(cpInfo, fmt.Sprintf("%s(%d/%d)", cp.Revision, cp.Available, cp.Replicas))
	}

	logger.Info("Istio upgrade — mesh state",
		"namespaces", len(namespaces),
		"controlPlanes", strings.Join(cpInfo, ","),
		"ingressGateways", len(gwStatuses),
		"webhooks", istioWebhooks,
		"configMaps", len(cms.Items),
	)
}

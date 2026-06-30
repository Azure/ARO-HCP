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

	_ "embed"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func NewKubeClient(kubeconfigPath string) (kubernetes.Interface, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}
	return kubernetes.NewForConfig(config)
}

//go:embed mesh-config.yaml
var meshConfig string

func revisionConfigMapName(revision string) string {
	return fmt.Sprintf("istio-shared-configmap-%s", revision)
}

func istiodServiceName(revision string) string {
	return fmt.Sprintf("istiod-%s", revision)
}

func CreateRevisionConfigMap(ctx context.Context, client kubernetes.Interface, revision string) error {
	logger := logr.FromContextOrDiscard(ctx).WithName("revision-configmap")
	cmName := revisionConfigMapName(revision)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: istioSystemNamespace,
			Labels:    map[string]string{"istio.io/rev": revision},
		},
		Data: map[string]string{"mesh": strings.TrimSpace(meshConfig)},
	}

	existing, err := client.CoreV1().ConfigMaps(istioSystemNamespace).Get(ctx, cmName, metav1.GetOptions{})
	if err == nil {
		if existing.Data["mesh"] == cm.Data["mesh"] && existing.Labels["istio.io/rev"] == revision {
			logger.Info("ConfigMap already up to date", "name", cmName)
			return nil
		}
		existing.Data = cm.Data
		if existing.Labels == nil {
			existing.Labels = make(map[string]string)
		}
		existing.Labels["istio.io/rev"] = revision
		if _, err = client.CoreV1().ConfigMaps(istioSystemNamespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update ConfigMap %s: %w", cmName, err)
		}
		logger.Info("ConfigMap updated", "name", cmName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get ConfigMap %s: %w", cmName, err)
	}

	if _, err = client.CoreV1().ConfigMaps(istioSystemNamespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create ConfigMap %s: %w", cmName, err)
	}
	logger.Info("ConfigMap created", "name", cmName)
	return nil
}

func DeleteRevisionConfigMap(ctx context.Context, client kubernetes.Interface, revision string) error {
	cmName := revisionConfigMapName(revision)
	if err := client.CoreV1().ConfigMaps(istioSystemNamespace).Delete(ctx, cmName, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete ConfigMap %s: %w", cmName, err)
	}
	return nil
}

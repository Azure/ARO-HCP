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

package verifiers

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type verifyPullSecretMergedIntoGlobal struct {
	expectedHost string
}

func (v verifyPullSecretMergedIntoGlobal) Name() string {
	return "VerifyPullSecretMergedIntoGlobal"
}

func (v verifyPullSecretMergedIntoGlobal) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	globalSecret, err := kubeClient.CoreV1().Secrets("kube-system").Get(ctx, "global-pull-secret", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get global-pull-secret: %w", err)
	}

	var globalConfig framework.DockerConfigJSON
	if err := json.Unmarshal(globalSecret.Data[corev1.DockerConfigJsonKey], &globalConfig); err != nil {
		return fmt.Errorf("failed to unmarshal global pull secret: %w", err)
	}

	if _, exists := globalConfig.Auths[v.expectedHost]; !exists {
		return fmt.Errorf("expected host %q not found in global pull secret", v.expectedHost)
	}

	return nil
}

func VerifyPullSecretMergedIntoGlobal(expectedHost string) HostedClusterVerifier {
	return verifyPullSecretMergedIntoGlobal{expectedHost: expectedHost}
}

type verifyGlobalPullSecretSyncer struct{}

func (v verifyGlobalPullSecretSyncer) Name() string {
	return "VerifyGlobalPullSecretSyncer"
}

func (v verifyGlobalPullSecretSyncer) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ds, err := kubeClient.AppsV1().DaemonSets("kube-system").Get(ctx, "global-pull-secret-syncer", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get global-pull-secret-syncer DaemonSet: %w", err)
	}

	// Verify the DaemonSet is ready - all desired pods are available
	if ds.Status.DesiredNumberScheduled == 0 {
		return fmt.Errorf("global-pull-secret-syncer has no desired pods scheduled")
	}
	if ds.Status.NumberReady != ds.Status.DesiredNumberScheduled {
		return fmt.Errorf("global-pull-secret-syncer not ready: %d/%d pods ready",
			ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)
	}

	return nil
}

func VerifyGlobalPullSecretSyncer() HostedClusterVerifier {
	return verifyGlobalPullSecretSyncer{}
}

type verifyPullSecretAuthData struct {
	secretName    string
	namespace     string
	expectedHost  string
	expectedAuth  string
	expectedEmail string
}

func (v verifyPullSecretAuthData) Name() string {
	return "VerifyPullSecretAuthData"
}

func (v verifyPullSecretAuthData) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	secret, err := kubeClient.CoreV1().Secrets(v.namespace).Get(ctx, v.secretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get secret %s/%s: %w", v.namespace, v.secretName, err)
	}

	var config framework.DockerConfigJSON
	if err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config); err != nil {
		return fmt.Errorf("failed to unmarshal pull secret: %w", err)
	}

	hostAuth, exists := config.Auths[v.expectedHost]
	if !exists {
		return fmt.Errorf("expected host %q not found in pull secret", v.expectedHost)
	}

	if hostAuth.Email != v.expectedEmail {
		return fmt.Errorf("expected email %q, got %q", v.expectedEmail, hostAuth.Email)
	}

	if hostAuth.Auth != v.expectedAuth {
		return fmt.Errorf("expected auth %q, got %q", v.expectedAuth, hostAuth.Auth)
	}

	return nil
}

func VerifyPullSecretAuthData(secretName, namespace, expectedHost, expectedAuth, expectedEmail string) HostedClusterVerifier {
	return verifyPullSecretAuthData{
		secretName:    secretName,
		namespace:     namespace,
		expectedHost:  expectedHost,
		expectedAuth:  expectedAuth,
		expectedEmail: expectedEmail,
	}
}

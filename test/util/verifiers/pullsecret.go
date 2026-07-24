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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type verifyPullSecretMergedIntoGlobal struct {
	expectedHost string
	timeout      time.Duration
}

func (v verifyPullSecretMergedIntoGlobal) Name() string {
	return fmt.Sprintf("VerifyPullSecretMergedIntoGlobal(%s)", v.expectedHost)
}

func (v verifyPullSecretMergedIntoGlobal) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		return v.checkOnce(ctx, adminRESTConfig)
	})
}

func (v verifyPullSecretMergedIntoGlobal) checkOnce(ctx context.Context, adminRESTConfig *rest.Config) error {
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

// VerifyPullSecretMergedIntoGlobal polls until the kube-system/global-pull-secret
// on the data plane contains an auths entry for expectedHost, or the timeout
// expires. The global-pull-secret is created by HCCO's Global Pull Secret
// Controller when it detects an additional-pull-secret in kube-system and
// merges it with the original-pull-secret.
//
// See https://hypershift.pages.dev/how-to/aws/global-pull-secret/
func VerifyPullSecretMergedIntoGlobal(expectedHost string, timeout time.Duration) HostedClusterVerifier {
	return verifyPullSecretMergedIntoGlobal{
		expectedHost: expectedHost,
		timeout:      timeout,
	}
}

const (
	globalPullSecretSyncerNamespace = "kube-system"
	globalPullSecretSyncerName      = "global-pull-secret-syncer"
)

// VerifyGlobalPullSecretSyncer verifies the global-pull-secret-syncer
// DaemonSet in kube-system is ready. Until this DaemonSet is ready, nodes
// will not have the updated pull secret credentials.
// It delegates to [VerifyDaemonSetReady].
//
// See https://hypershift.pages.dev/how-to/aws/global-pull-secret/
func VerifyGlobalPullSecretSyncer(timeout time.Duration) HostedClusterVerifier {
	return VerifyDaemonSetReady(globalPullSecretSyncerNamespace, globalPullSecretSyncerName, timeout)
}

type verifyPullSecretAuthData struct {
	secretName    string
	namespace     string
	expectedHost  string
	expectedAuth  string
	expectedEmail string
}

func (v verifyPullSecretAuthData) Name() string {
	return fmt.Sprintf("VerifyPullSecretAuthData(%s/%s:%s)", v.namespace, v.secretName, v.expectedHost)
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

// VerifyPullSecretAuthData performs a single-shot check that the named
// dockerconfigjson Secret contains the expected auth (base64-encoded
// "username:password") and email values for the given registry host.
// Call only after HCCO has finished merging (e.g. after
// [VerifyPullSecretMergedIntoGlobal] succeeds).
func VerifyPullSecretAuthData(secretName, namespace, expectedHost, expectedAuth, expectedEmail string) HostedClusterVerifier {
	return verifyPullSecretAuthData{
		secretName:    secretName,
		namespace:     namespace,
		expectedHost:  expectedHost,
		expectedAuth:  expectedAuth,
		expectedEmail: expectedEmail,
	}
}

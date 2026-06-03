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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyDaemonSetReady struct {
	namespace string
	name      string
	wait      waitSettings
}

func (v verifyDaemonSetReady) Name() string {
	return daemonSetVerifierName(v.namespace, v.name)
}

func daemonSetVerifierName(namespace, name string) string {
	return fmt.Sprintf("VerifyDaemonSetReady(%s/%s)", namespace, name)
}

func (v verifyDaemonSetReady) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	return verifyOnceOrPoll(ctx, v.Name(), adminRESTConfig, v.wait, nil, v.checkOnce)
}

func (v verifyDaemonSetReady) checkOnce(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ds, err := kubeClient.AppsV1().DaemonSets(v.namespace).Get(ctx, v.name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get DaemonSet %s/%s: %w", v.namespace, v.name, err)
	}

	if ds.Status.DesiredNumberScheduled == 0 {
		return fmt.Errorf("DaemonSet %s/%s has no desired pods scheduled", v.namespace, v.name)
	}
	if ds.Status.NumberReady != ds.Status.DesiredNumberScheduled {
		return fmt.Errorf("DaemonSet %s/%s not ready: %d/%d pods ready",
			v.namespace, v.name, ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)
	}

	return nil
}

// VerifyDaemonSetReady verifies that a DaemonSet exists, has at least one desired pod, and
// NumberReady equals DesiredNumberScheduled. Pass [WithWait] when the DaemonSet may not exist
// or become ready immediately; omit wait options for a single-shot check.
func VerifyDaemonSetReady(namespace, name string, opts ...WaitOption) HostedClusterVerifier {
	return verifyDaemonSetReady{
		namespace: namespace,
		name:      name,
		wait:      applyWaitOptions(opts),
	}
}

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
	"errors"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type HostedClusterVerifier interface {
	Name() string
	Verify(ctx context.Context, restConfig *rest.Config) error
}

// ExpectForbidden returns a verifier that runs inner and succeeds only when inner.Verify
// returns a forbidden (403) error. Use it to assert that an identity must not be able
// to perform the action implemented by inner, without defining separate "Cannot" verifiers.
func ExpectForbidden(inner HostedClusterVerifier) HostedClusterVerifier {
	return expectForbidden{inner: inner}
}

type expectForbidden struct {
	inner HostedClusterVerifier
}

func (w expectForbidden) Name() string {
	return fmt.Sprintf("ExpectForbidden(%s)", w.inner.Name())
}

func (w expectForbidden) Verify(ctx context.Context, restConfig *rest.Config) error {
	err := w.inner.Verify(ctx, restConfig)
	if err == nil {
		return fmt.Errorf("expected forbidden, but %s succeeded", w.inner.Name())
	}
	if !apierrors.IsForbidden(err) {
		return fmt.Errorf("expected forbidden from %s, got: %w", w.inner.Name(), err)
	}
	return nil
}

type verifyImageRegistryDisabled struct{}

func (v verifyImageRegistryDisabled) Name() string {
	return "VerifyImageRegistryDisabled"
}

func (v verifyImageRegistryDisabled) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	_, err = kubeClient.CoreV1().Services("openshift-image-registry").Get(ctx, "image-registry", metav1.GetOptions{})
	if err == nil {
		return fmt.Errorf("image-registry service should not exist, but it does")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("wrong type of error: %T, %v", err, err)
	}

	_, err = kubeClient.AppsV1().Deployments("openshift-image-registry").Get(ctx, "image-registry", metav1.GetOptions{})
	if err == nil {
		return fmt.Errorf("image-registry deployment should not exist, but it does")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("wrong type of error: %T, %v", err, err)
	}

	return nil
}

func VerifyImageRegistryDisabled() HostedClusterVerifier {
	return verifyImageRegistryDisabled{}
}

type verifyBasicAccessImpl struct{}

func (v verifyBasicAccessImpl) Name() string {
	return "VerifyBasicAccess"
}

func (v verifyBasicAccessImpl) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	_, err = kubeClient.CoreV1().Services("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	return nil
}

func verifyBasicAccess() HostedClusterVerifier {
	return verifyBasicAccessImpl{}
}

var standardVerifiers = []HostedClusterVerifier{
	verifyBasicAccess(),
	verifyAllAPIServicesAvailable(),
}

func VerifyHCPCluster(ctx context.Context, adminRESTConfig *rest.Config, additionalVerifiers ...HostedClusterVerifier) error {
	allVerifiers := append(standardVerifiers, additionalVerifiers...)

	errCh := make(chan error, len(allVerifiers))
	wg := sync.WaitGroup{}
	for _, verifier := range allVerifiers {
		wg.Add(1)
		go func(ctx context.Context, verifier HostedClusterVerifier) {
			defer wg.Done()
			err := verifier.Verify(ctx, adminRESTConfig)
			if err != nil {
				errCh <- fmt.Errorf("%v failed: %w", verifier.Name(), err)
			}
		}(ctx, verifier)
	}
	wg.Wait()
	close(errCh)

	errs := []error{}
	for err := range errCh {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

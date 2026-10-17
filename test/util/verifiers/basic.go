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

// verifyAll runs every supplied verifier in parallel and joins their errors.
//
// It is deliberately unexported: VerifyHCPCluster is the single entry point tests use to run
// independent verifiers in parallel (see test/AGENTS.md).
func verifyAll(ctx context.Context, adminRESTConfig *rest.Config, allVerifiers ...HostedClusterVerifier) error {
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

// VerifyHCPCluster runs the standard cluster viability verifiers, plus any additional verifiers,
// in parallel. It is the single entry point for running independent verifiers concurrently (see
// test/AGENTS.md).
//
// Pass only verifiers that are independent of the standard viability set and that settle on a
// comparable timescale. The standard verifiers are single-shot or bounded well below a long wait
// -- verifyBasicAccess lists services once with no retry, verifyAllAPIServicesAvailable gives up
// after 5 minutes -- so an additional verifier that polls for tens of minutes does not extend
// them. It leaves them having sampled the cluster once, at the start of that wait, which asserts
// viability against the cluster as it was before whatever the wait was for, and turns a transient
// error at t=0 into a failed spec even if the API recovers seconds later.
//
// A verifier that waits that long belongs in a phase of its own, called one at a time with
// Expect(verifier.Verify(ctx, adminRESTConfig)).NotTo(HaveOccurred(), "..."), with VerifyHCPCluster
// called afterwards to check viability once the wait has resolved. The control plane upgrade specs
// are the worked example; ARO-26775 is what the other shape costs.
func VerifyHCPCluster(ctx context.Context, adminRESTConfig *rest.Config, additionalVerifiers ...HostedClusterVerifier) error {
	// Build a fresh slice rather than appending to standardVerifiers, which would let one
	// caller write into a package-level backing array shared by parallel specs.
	allVerifiers := make([]HostedClusterVerifier, 0, len(standardVerifiers)+len(additionalVerifiers))
	allVerifiers = append(allVerifiers, standardVerifiers...)
	allVerifiers = append(allVerifiers, additionalVerifiers...)

	return verifyAll(ctx, adminRESTConfig, allVerifiers...)
}

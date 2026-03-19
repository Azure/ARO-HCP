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

package verifiers

import (
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

// ImageDigestMirrorExpectation describes an expected source-to-mirrors mapping
// that should exist in the cluster's ImageDigestMirrorSet objects.
type ImageDigestMirrorExpectation struct {
	Source             string
	Mirrors            []configv1.ImageMirror
	MirrorSourcePolicy configv1.MirrorSourcePolicy
	// AbsentSources lists sources that must NOT exist in any IDMS object on the cluster.
	AbsentSources []string
}

type verifyImageDigestMirrorSets struct {
	expectedMirrors []ImageDigestMirrorExpectation
}

func (v verifyImageDigestMirrorSets) Name() string {
	return "VerifyImageDigestMirrorSets"
}

func (v verifyImageDigestMirrorSets) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	idmsList, err := configClient.ImageDigestMirrorSets().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ImageDigestMirrorSets: %w", err)
	}

	if len(idmsList.Items) == 0 {
		return fmt.Errorf("no ImageDigestMirrorSets found on the cluster")
	}

	// Collect all source→mirrors mappings from all IDMS objects
	foundMirrors := map[string]configv1.ImageDigestMirrors{}
	for _, item := range idmsList.Items {
		for _, entry := range item.Spec.ImageDigestMirrors {
			foundMirrors[entry.Source] = entry
		}
	}

	// Verify each expected mirror exists
	var failures []string
	for _, expected := range v.expectedMirrors {
		actualMirrors, sourceFound := foundMirrors[expected.Source]
		if !sourceFound {
			failures = append(failures, fmt.Sprintf("source %q not found in any IDMS", expected.Source))
			continue
		}
		for _, expectedMirror := range expected.Mirrors {
			if !slices.Contains(actualMirrors.Mirrors, expectedMirror) {
				failures = append(failures, fmt.Sprintf("mirror %q not found for source %q (found: %v)", expectedMirror, expected.Source, actualMirrors.Mirrors))
			}
		}
		if actualMirrors.MirrorSourcePolicy != expected.MirrorSourcePolicy {
			failures = append(failures, fmt.Sprintf("expected mirror source policy %q for source %q (found: %v)", expected.MirrorSourcePolicy, expected.Source, actualMirrors.MirrorSourcePolicy))
		}
	}

	// Verify absent sources do not exist
	foundSources := make([]string, 0, len(foundMirrors))
	for src := range foundMirrors {
		foundSources = append(foundSources, src)
	}
	for _, expected := range v.expectedMirrors {
		for _, absentSource := range expected.AbsentSources {
			if slices.Contains(foundSources, absentSource) {
				failures = append(failures, fmt.Sprintf("source %q should be absent but was found in IDMS", absentSource))
			}
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("IDMS verification failed:\n%s", strings.Join(failures, "\n"))
	}

	return nil
}

// VerifyImageDigestMirrorSets returns a HostedClusterVerifier that checks whether
// the cluster contains ImageDigestMirrorSet objects matching the expected source→mirrors mappings.
func VerifyImageDigestMirrorSets(expectedMirrors []ImageDigestMirrorExpectation) HostedClusterVerifier {
	return verifyImageDigestMirrorSets{expectedMirrors: expectedMirrors}
}

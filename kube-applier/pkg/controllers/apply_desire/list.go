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

package apply_desire

import (
	"context"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// clusterIDFromNamespace is called only after the namespace grammar has been
// validated by RelateHostedClusterNamespace.
func clusterIDFromNamespace(namespace string) string {
	return namespace[strings.LastIndexByte(namespace, '-')+1:]
}

// listAll reads every page. A partial list is never evidence that a layer is
// absent, so callers only advance after the complete list succeeds.
func listAll(ctx context.Context, resource dynamic.ResourceInterface) ([]unstructured.Unstructured, error) {
	var all []unstructured.Unstructured
	continueToken := ""
	for {
		page, err := resource.List(ctx, metav1.ListOptions{Limit: 500, Continue: continueToken})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		continueToken = page.GetContinue()
		if len(continueToken) == 0 {
			return all, nil
		}
	}
}

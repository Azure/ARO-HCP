// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package main

import (
	"slices"
	"strings"

	"github.com/Azure/ARO-HCP/hack/verify-controller-watches/analyzer"
)

func baselineContent(result *analyzer.Result) string {
	var identities []string
	for _, finding := range result.Findings {
		identities = append(identities, finding.Identity())
	}
	slices.Sort(identities)
	identities = slices.Compact(identities)
	return "# Controller watch invariant baseline. Regenerate with make update-controller-watches.\n# Identity only: controller | invariant | fully-qualified Go document type.\n" + strings.Join(identities, "\n") + "\n"
}

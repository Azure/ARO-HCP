// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package analyzer

import (
	"go/types"
	"strings"
)

// These packages define the trusted typed storage and informer contracts. A
// helper returning a client is NOT itself an endpoint: its body may reach a
// different document's lister before returning that client.
func endpointPackage(path string) bool {
	return strings.HasPrefix(path, repo+"internal/database/cosmosstorage/") ||
		strings.HasPrefix(path, repo+"internal/database/listers/") ||
		strings.HasPrefix(path, repo+"internal/database/informers/") ||
		strings.HasPrefix(path, repo+"internal/database/unioninformers/")
}

// Import reachability rules out direct calls into the storage contracts, not
// indirect calls through callbacks supplied by a controller. The evaluator
// follows those separately when pruning an unrelated package's body.
func reachesEndpoints(pkg *types.Package, memo map[string]bool) bool {
	if reaches, ok := memo[pkg.Path()]; ok {
		return reaches
	}
	reaches := endpointPackage(pkg.Path())
	for _, dep := range pkg.Imports() {
		reaches = reachesEndpoints(dep, memo) || reaches
	}
	memo[pkg.Path()] = reaches
	return reaches
}

func accessMethod(name string) bool {
	return name == "Get" || name == "GetByID" ||
		(strings.HasPrefix(name, "List") && name != "Listers") ||
		name == "Create" || name == "Replace" || name == "Delete" ||
		strings.HasPrefix(name, "Patch") ||
		name == "AddCreateToTransaction" || name == "AddReplaceToTransaction" ||
		name == "DeleteByCosmosID"
}

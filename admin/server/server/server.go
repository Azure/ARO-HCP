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

package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Azure/azure-kusto-go/kusto"

	"github.com/Azure/ARO-HCP/admin/server/handlers"
	"github.com/Azure/ARO-HCP/admin/server/handlers/cosmosdump"
	"github.com/Azure/ARO-HCP/admin/server/handlers/hcp"
	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type AdminAPI struct {
	clustersServiceClient  ocm.ClusterServiceClientSpec
	dbClient               database.DBClient
	kustoClient            *kusto.Client
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever

	location string
	logger   *slog.Logger
}

func NewAdminAPI(
	location string,
	dbClient database.DBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	kustoClient *kusto.Client,
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
) *AdminAPI {
	return &AdminAPI{
		location:               location,
		dbClient:               dbClient,
		clustersServiceClient:  clustersServiceClient,
		kustoClient:            kustoClient,
		fpaCredentialRetriever: fpaCredentialRetriever,
	}
}

// NewAdminHandler creates an http.Handler for the admin API with all middleware configured.
func (a *AdminAPI) Handlers(ctx context.Context) http.Handler {
	// Submux for V1 HCP endpoints
	v1HCPMux := middleware.NewHCPResourceServerMux()
	v1HCPMux.Handle("GET", "/helloworld", hcp.HCPHelloWorld(a.dbClient, a.clustersServiceClient))
	v1HCPMux.Handle("GET", "/hellworld/lbs", hcp.HCPDemoListLoadbalancers(a.dbClient, a.clustersServiceClient, a.fpaCredentialRetriever))

	v1HCPMux.Handle("GET", "/cosmosdump", cosmosdump.NewCosmosDumpHandler(a.dbClient))

	rootMux := http.NewServeMux()
	rootMux.Handle("/admin/helloworld", handlers.HelloWorldHandler())
	rootMux.Handle("/admin/v1/hcp/", http.StripPrefix("/admin/v1/hcp", v1HCPMux.Handler()))

	return middleware.WithClientPrincipal(middleware.WithLowercaseURLPathValue(middleware.WithLogger(ctx, rootMux)))
}

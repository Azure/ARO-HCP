package handlers

import (
	"net/http"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/go-logr/logr"
)

func StartSessionHandler(logger logr.Logger,
	dbClient *database.DBClient,
	csClient *ocm.ClusterServiceClientSpec,
	sessionSvc SessionService) http.Handler {

}

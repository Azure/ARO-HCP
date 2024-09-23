package admin

import (
	"net/http"
	"strings"
)

func (a *Admin) adminRoutes() *http.ServeMux {

	adminMux := http.NewServeMux()

	adminMux.HandleFunc("/v1/ocm/clusters/id/", func(writer http.ResponseWriter, request *http.Request) {
		// Extract ID from the URL
		id := strings.TrimPrefix(request.URL.Path, "/v1/ocm/clusters/id/")
		if id == "" {
			http.Error(writer, "Cluster ID not provided", http.StatusBadRequest)
			return
		}
		q := request.URL.Query()
		q.Add("id", id)
		request.URL.RawQuery = q.Encode()
		a.AdminClustersListFromCS(writer, request)
	})

	adminMux.HandleFunc("/v1/ocm/clusters", func(writer http.ResponseWriter, request *http.Request) {
		a.AdminClustersListFromCS(writer, request)
	})

	return adminMux
}

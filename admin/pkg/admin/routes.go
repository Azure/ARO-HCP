package admin

import (
	"net/http"
)

func (a *Admin) adminRoutes() *http.ServeMux {

	adminMux := http.NewServeMux()

	adminMux.HandleFunc("/v1/<something>/", func(writer http.ResponseWriter, request *http.Request) {
		// Queries something
	})

	adminMux.HandleFunc("/v1/<something>", func(writer http.ResponseWriter, request *http.Request) {
		// Queries something
	})

	return adminMux
}

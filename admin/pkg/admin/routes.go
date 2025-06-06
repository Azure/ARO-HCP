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

package admin

import (
	"fmt"
	"net/http"
)

func (a *Admin) adminRoutes() *http.ServeMux {

	adminMux := http.NewServeMux()

	adminMux.HandleFunc("/admin/helloworld", func(writer http.ResponseWriter, request *http.Request) {

		// Return Hello, world! to the client
		fmt.Fprintln(writer, "Hello, world!")
	})

	adminMux.HandleFunc("/v1/<something>", func(writer http.ResponseWriter, request *http.Request) {
		// Queries something
	})

	return adminMux
}

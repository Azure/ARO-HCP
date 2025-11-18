package handlers

import (
	"fmt"
	"net/http"
)

func HelloWorldHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprintln(writer, "Hello, world!")
	})
}

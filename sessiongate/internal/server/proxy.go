package server

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/proxy"
	"k8s.io/client-go/rest"
)

// newKASProxyHandler returns a handler that:
//   - Accepts requests under the specified stripPrefix
//   - Removes the prefix from the request path
//   - Builds a backend location for the Kube API server
//   - Invokes an UpgradeAwareHandler per request
func newKASProxyHandler(
	restCfg *rest.Config,
	sessionID string,
	stripPathPrefix string,
) (http.Handler, error) {
	backendBase, err := url.Parse(restCfg.Host)
	if err != nil {
		return nil, err
	}

	transport, err := rest.TransportFor(restCfg)
	if err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())

		path := r.URL.Path

		if !strings.HasPrefix(path, stripPathPrefix) {
			http.NotFound(w, r)
			return
		}

		restPath := strings.TrimPrefix(path, stripPathPrefix)
		if restPath == "" {
			restPath = "/"
		}

		if !strings.HasPrefix(restPath, "/") {
			restPath = "/" + restPath
		}

		location := *backendBase
		location.Path = utilnet.JoinPreservingTrailingSlash(location.Path, restPath)
		location.RawQuery = r.URL.RawQuery

		log.Printf("[PROXY] backend target: %s (restPath=%s query=%s)",
			location.String(), restPath, location.RawQuery)

		//handler := newUpgradeAwareProxyHandler(&location, transport, true, false)
		handler := proxy.NewUpgradeAwareHandler(&location, transport, true, false, &sessionErrorResponder{sessionID: sessionID})

		handler.ServeHTTP(w, r)
	}), nil
}

// sessionErrorResponder implements proxy.ErrorResponder with session-specific context
type sessionErrorResponder struct {
	sessionID string
}

func (r *sessionErrorResponder) Error(w http.ResponseWriter, req *http.Request, err error) {
	http.Error(w, fmt.Sprintf("Proxy request failed for session %s: %v", r.sessionID, err), http.StatusBadGateway)
}

package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
)

// MiddlewareReferer ensures a Referer header is present in the request.
// This header is always added by ARM but is often forgotten in testing
// environments. If missing, derive a Referer from the http.Request.
//
// Referer headers are used in a few places:
// - The "nextLink" field in a paginated response body
// - "Location" and "Azure-AsyncOperation" response headers
func MiddlewareReferer(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if r.Referer() == "" && r.URL != nil {
		var refererURL = *r.URL

		if refererURL.Scheme == "" {
			if r.TLS != nil {
				refererURL.Scheme = "https"
			} else {
				refererURL.Scheme = "http"
			}
		}

		if refererURL.Host == "" {
			refererURL.Host = r.Host
		}

		// Referer headers never include fragments or userinfo.
		// https://datatracker.ietf.org/doc/html/rfc7231#section-5.5.2
		refererURL.User = nil
		refererURL.Fragment = ""

		r.Header.Set("Referer", refererURL.String())
	}

	next(w, r)
}

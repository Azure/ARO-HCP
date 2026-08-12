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

package util

import (
	"io"
	"math/rand"
	nethttp "net/http"
	"path"
	"strconv"
	"time"

	kiotahttp "github.com/microsoft/kiota-http-go"
)

const (
	graphRetryMaxRetries         = 10
	graphRetryBaseDelaySeconds   = 5
	graphRetryMaxCumulativeDelay = 120 * time.Second
)

// graphRetryHandler is a kiota middleware that retries transient Graph API errors.
// It replaces kiota's default RetryHandler to add 404 retries on POST requests,
// which handles eventual-consistency delays after resource creation.
type graphRetryHandler struct{}

func newGraphRetryHandler() *graphRetryHandler {
	return &graphRetryHandler{}
}

func (h *graphRetryHandler) Intercept(pipeline kiotahttp.Pipeline, middlewareIndex int, req *nethttp.Request) (*nethttp.Response, error) {
	resp, err := pipeline.Next(req, middlewareIndex)
	if err != nil {
		return resp, err
	}
	return h.retryIfNeeded(pipeline, middlewareIndex, req, resp, 0, 0)
}

func (h *graphRetryHandler) retryIfNeeded(pipeline kiotahttp.Pipeline, middlewareIndex int, req *nethttp.Request, resp *nethttp.Response, executionCount int, cumulativeDelay time.Duration) (*nethttp.Response, error) {
	if !h.isRetriableStatusCode(resp.StatusCode, req) ||
		!h.isRetriableRequest(req) ||
		executionCount >= graphRetryMaxRetries ||
		cumulativeDelay >= graphRetryMaxCumulativeDelay {
		return resp, nil
	}

	executionCount++
	delay := h.getRetryDelay(resp, executionCount)
	cumulativeDelay += delay

	if cumulativeDelay > graphRetryMaxCumulativeDelay {
		return resp, nil
	}

	req.Header.Set("Retry-Attempt", strconv.Itoa(executionCount))

	if req.Body != nil {
		if s, ok := req.Body.(io.Seeker); ok {
			if _, err := s.Seek(0, io.SeekStart); err != nil {
				return resp, err
			}
		} else if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return resp, err
			}
			req.Body = body
		}
	}

	resp.Body.Close()

	ctx := req.Context()
	t := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		t.Stop()
		return nil, ctx.Err()
	case <-t.C:
	}

	response, err := pipeline.Next(req, middlewareIndex)
	if err != nil {
		return response, err
	}
	return h.retryIfNeeded(pipeline, middlewareIndex, req, response, executionCount, cumulativeDelay)
}

func (h *graphRetryHandler) isRetriableStatusCode(code int, req *nethttp.Request) bool {
	switch code {
	case nethttp.StatusTooManyRequests, nethttp.StatusServiceUnavailable, nethttp.StatusGatewayTimeout:
		return true
	case nethttp.StatusNotFound:
		// 404 on POST indicates eventual-consistency delay (e.g. AddPassword
		// right after app creation). GET/DELETE 404 is a real "not found".
		return req.Method == nethttp.MethodPost
	case nethttp.StatusBadRequest:
		// Graph returns 400 Request_BadRequest with detail code
		// NoBackingApplicationObject on POST /servicePrincipals when the
		// just-created app registration has not yet replicated. Scope the
		// retry to that endpoint so genuinely bad requests elsewhere stay
		// fatal instead of being delayed by up to graphRetryMaxCumulativeDelay.
		return req.Method == nethttp.MethodPost && path.Base(req.URL.Path) == "servicePrincipals"
	case nethttp.StatusForbidden:
		// Graph returns 403 Authorization_RequestDenied due to Entra ID
		// eventual consistency when operating on recently created resources:
		//  - "backing application must be in the local tenant" on POST /servicePrincipals
		//  - "Insufficient privileges" on POST /applications/{id}/addPassword
		// Same race as the 400 case above (Azure CLI #18610, Vault #49, Terraform #992).
		basePath := path.Base(req.URL.Path)
		return req.Method == nethttp.MethodPost &&
			(basePath == "servicePrincipals" || basePath == "addPassword")
	default:
		return false
	}
}

func (h *graphRetryHandler) isRetriableRequest(req *nethttp.Request) bool {
	isBodiedMethod := req.Method == nethttp.MethodPost || req.Method == nethttp.MethodPut || req.Method == nethttp.MethodPatch
	if isBodiedMethod && req.Body != nil && req.Body != nethttp.NoBody {
		if req.ContentLength == -1 {
			return false
		}
		_, isSeeker := req.Body.(io.Seeker)
		return isSeeker || req.GetBody != nil
	}
	return true
}

func (h *graphRetryHandler) getRetryDelay(resp *nethttp.Response, executionCount int) time.Duration {
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil {
			return max(0, time.Duration(seconds*float64(time.Second)))
		}
		if t, err := time.Parse(time.RFC1123, retryAfter); err == nil {
			return max(0, time.Until(t))
		}
	}
	exp := executionCount - 1
	delay := time.Duration(graphRetryBaseDelaySeconds*(1<<exp)) * time.Second
	jitter := time.Duration(rand.Float64()*1000) * time.Millisecond
	return delay + jitter
}

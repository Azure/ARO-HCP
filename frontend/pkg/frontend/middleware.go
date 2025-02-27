package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"container/list"
	"net/http"
)

// MiddlewareFunc specifies the call signature for middleware functions.
// At some point during normal execution, the middleware function must call
// the "next" handler function to invoke the next layer of request handling.
type MiddlewareFunc func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc)

// Middleware is a list of middleware functions to execute before invoking an http.Handler.
type Middleware struct {
	functions list.List
}

// NewMiddleware allocates and returns a new Middleware.
func NewMiddleware(functions ...MiddlewareFunc) *Middleware {
	m := &Middleware{}
	m.init(functions...)
	return m
}

func (m *Middleware) init(functions ...MiddlewareFunc) {
	for _, item := range functions {
		m.functions.PushBack(item)
	}
}

// nextMiddleware returns the function that middleware functions receive as
// their "next" argument. The returned function invokes the next middleware
// function in the list if one exists, or else the final HTTP handler.
func (m *Middleware) nextMiddleware(el *list.Element, handler http.Handler) http.HandlerFunc {
	if el != nil {
		return func(w http.ResponseWriter, r *http.Request) {
			el.Value.(MiddlewareFunc)(w, r, m.nextMiddleware(el.Next(), handler))
		}
	}
	return handler.ServeHTTP
}

// Handler returns an http.Handler that invokes the list of middleware
// functions before invoking the given HTTP handler. Pass the returned
// http.Handler to http.ServeMux.Handle to add middleware functions that
// execute after pattern-based multiplexing occurs and values for path
// wildcards are available via http.Request.PathValue.
func (m *Middleware) Handler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.nextMiddleware(m.functions.Front(), handler)(w, r)
	})
}

// HandlerFunc returns an http.Handler that invokes the list of middleware
// functions before invoking the given HTTP handler function. Pass the returned
// http.Handler to http.ServeMux.Handle to add middleware functions that
// execute after pattern-based multiplexing occurs and values for path
// wildcards are available via http.Request.PathValue.
func (m *Middleware) HandlerFunc(handler func(http.ResponseWriter, *http.Request)) http.Handler {
	return m.Handler(http.HandlerFunc(handler))
}

// MiddlewareMux is an http.ServeMux with middleware functions that execute
// before pattern-based multiplexing occurs.
type MiddlewareMux struct {
	http.ServeMux
	middleware Middleware
}

// NewMiddlewareMux allocates and returns a new MiddlewareMux.
func NewMiddlewareMux(functions ...MiddlewareFunc) *MiddlewareMux {
	mux := &MiddlewareMux{}
	mux.middleware.init(functions...)
	return mux
}

// ServeHTTP dispatches the request to each middleware function, and then to
// the handler whose pattern most closely matches the request URL.
func (mux *MiddlewareMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Initialize a string pointer to record the pattern matched by ServeMux.
	//
	// This is useful for middlewares that are executed before ServeMux and
	// want to know the matched pattern. They can't rely on the value stored in
	// r.Pattern because the original request can be mutated by following
	// middlewares in which case r.Pattern remains empty.
	//
	// Since the handlers execute sequentially, there's no risk of concurrent
	// access to the value.
	patt := new(string)
	r = r.WithContext(ContextWithPattern(r.Context(), patt))

	mainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeMux.ServeHTTP(w, r)
		*patt = r.Pattern
	})

	mux.middleware.Handler(mainHandler).ServeHTTP(w, r)
}

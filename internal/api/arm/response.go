package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"net/url"
)

// PagedResponse is the response format for resource collection requests.
type PagedResponse struct {
	Value    []json.RawMessage `json:"value"`
	NextLink string            `json:"nextLink,omitempty"`
}

// AddValue adds a JSON encoded value to a PagedResponse.
func (r *PagedResponse) AddValue(value json.RawMessage) {
	r.Value = append(r.Value, value)
}

// SetNextLink sets NextLink to a URL with a $skipToken parameter.
// If skipToken is empty, the function does nothing and returns nil.
func (r *PagedResponse) SetNextLink(baseURL, skipToken string) error {
	if skipToken == "" {
		return nil
	}

	u, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return err
	}

	values := u.Query()
	values.Set("$skipToken", skipToken)
	u.RawQuery = values.Encode()

	r.NextLink = u.String()
	return nil
}

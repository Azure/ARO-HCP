package api

import (
	"slices"
)

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

// Ptr returns a pointer to p.
func Ptr[T any](p T) *T {
	return &p
}

// DeleteNilsFromPtrSlice returns a slice with nil pointers removed.
func DeleteNilsFromPtrSlice[S ~[]*E, E any](s S) S {
	return slices.DeleteFunc(s, func(e *E) bool { return e == nil })
}

// StringSliceToStringPtrSlice converts a slice of strings to a slice of string pointers.
func StringSliceToStringPtrSlice(s []string) []*string {
	out := make([]*string, len(s))
	for index, item := range s {
		out[index] = Ptr(item)
	}
	return out
}

// StringPtrSliceToStringSlice converts a slice of string pointers to a slice of strings.
func StringPtrSliceToStringSlice(s []*string) []string {
	s = DeleteNilsFromPtrSlice(s)
	out := make([]string, 0, len(s))
	for _, item := range s {
		out = append(out, *item)
	}
	return out
}

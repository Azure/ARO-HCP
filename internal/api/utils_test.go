package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMergeStringPtrMap(t *testing.T) {
	tests := []struct {
		name   string
		src    map[string]*string
		dst    map[string]string
		expect map[string]string
	}{
		{
			name:   "No source map and no destination map",
			src:    nil,
			dst:    nil,
			expect: nil,
		},
		{
			name: "No source map but existing destination map",
			src:  nil,
			dst: map[string]string{
				"Blinky": "Shadow",
			},
			expect: map[string]string{
				"Blinky": "Shadow",
			},
		},
		{
			name: "Add entry to a new map",
			src: map[string]*string{
				"Blinky": Ptr("Shadow"),
			},
			dst: nil,
			expect: map[string]string{
				"Blinky": "Shadow",
			},
		},
		{
			name: "Add entry to an existing map",
			src: map[string]*string{
				"Blinky": Ptr("Shadow"),
			},
			dst: map[string]string{
				"Pinky": "Speedy",
				"Inky":  "Bashful",
				"Clyde": "Pokey",
			},
			expect: map[string]string{
				"Blinky": "Shadow",
				"Pinky":  "Speedy",
				"Inky":   "Bashful",
				"Clyde":  "Pokey",
			},
		},
		{
			name: "Delete entry from a non-existent map",
			src: map[string]*string{
				"Blinky": nil,
			},
			dst:    nil,
			expect: nil,
		},
		{
			name: "Delete entry from an existing map",
			src: map[string]*string{
				"Blinky": nil,
			},
			dst: map[string]string{
				"Blinky": "Shadow",
				"Pinky":  "Speedy",
				"Inky":   "Bashful",
				"Clyde":  "Pokey",
			},
			expect: map[string]string{
				"Pinky": "Speedy",
				"Inky":  "Bashful",
				"Clyde": "Pokey",
			},
		},
		{
			name: "Both add and delete entries from an existing map",
			src: map[string]*string{
				"Blinky": nil,
				"Pinky":  nil,
				"Inky":   Ptr("Bashful"),
				"Clyde":  Ptr("Pokey"),
			},
			dst: map[string]string{
				"Blinky": "Shadow",
				"Inky":   "Bashful",
			},
			expect: map[string]string{
				"Inky":  "Bashful",
				"Clyde": "Pokey",
			},
		},
		{
			name: "Modify an entry in an existing map",
			src: map[string]*string{
				"Blinky": Ptr("Oikake"),
			},
			dst: map[string]string{
				"Blinky": "Shadow",
				"Inky":   "Bashful",
			},
			expect: map[string]string{
				"Blinky": "Oikake",
				"Inky":   "Bashful",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			MergeStringPtrMap(tt.src, &tt.dst)
			if !reflect.DeepEqual(tt.expect, tt.dst) {
				t.Error(cmp.Diff(tt.expect, tt.dst))
			}
		})
	}
}

func TestNonNilSliceValues(t *testing.T) {
	a, b, c := Ptr("A"), Ptr("B"), Ptr("C")
	testCases := []struct {
		name string
		s    []*string
		want []*string
	}{
		{name: "nil slice", s: nil, want: nil},
		{name: "empty slice", s: []*string{}, want: nil},
		{name: "no nil", s: []*string{a, b, c}, want: []*string{a, b, c}},
		{name: "nil start", s: []*string{nil, a, b, c}, want: []*string{a, b, c}},
		{name: "nil end", s: []*string{a, b, c, nil}, want: []*string{a, b, c}},
		{name: "nil mid", s: []*string{a, b, nil, c}, want: []*string{a, b, c}},
		{name: "all nil", s: []*string{nil, nil, nil, nil}, want: nil},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var got []*string
			for _, x := range NonNilSliceValues(tc.s) {
				got = append(got, x)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("NonNilSliceValues() = %v, want %v", got, tc.want)
			}
		})
	}
}

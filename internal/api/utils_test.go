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

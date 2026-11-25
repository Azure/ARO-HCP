// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0.

package dependabot

import (
	"encoding/hex"
	"fmt"
)

// Native publication Unicode validation rejects the lone UTF-16 surrogates encoding/json otherwise silently replaces.
func nativePublicationJSONUnicode(payload []byte) error {
	for i := 0; i < len(payload); i++ {
		if payload[i] != '\\' {
			continue
		}
		if i+1 >= len(payload) {
			return fmt.Errorf("truncated JSON escape")
		}
		if payload[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(payload) {
			return fmt.Errorf("truncated JSON Unicode escape")
		}
		var code [2]byte
		if _, err := hex.Decode(code[:], payload[i+2:i+6]); err != nil {
			return fmt.Errorf("invalid JSON Unicode escape: %w", err)
		}
		value := uint16(code[0])<<8 | uint16(code[1])
		switch {
		case value >= 0xdc00 && value <= 0xdfff:
			return fmt.Errorf("unpaired JSON low surrogate")
		case value >= 0xd800 && value <= 0xdbff:
			if i+12 > len(payload) || payload[i+6] != '\\' || payload[i+7] != 'u' {
				return fmt.Errorf("unpaired JSON high surrogate")
			}
			if _, err := hex.Decode(code[:], payload[i+8:i+12]); err != nil {
				return fmt.Errorf("invalid JSON surrogate pair: %w", err)
			}
			low := uint16(code[0])<<8 | uint16(code[1])
			if low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("unpaired JSON high surrogate")
			}
			i += 11
		default:
			i += 5
		}
	}
	return nil
}

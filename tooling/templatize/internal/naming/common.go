package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func suffixDigest(length int, strs ...string) (string, error) {
	combined := ""
	for _, s := range strs {
		combined += s
	}
	hash := sha256.Sum256([]byte(combined))
	hashedString := hex.EncodeToString(hash[:])
	if len(hashedString) < length {
		return "", fmt.Errorf("suffix digest does not have the required length of %d", length)
	}
	return hashedString[:length], nil
}

func suffixedName(prefix string, suffixDelim string, maxLength int, suffixLength int, suffixDigestArgs ...string) (string, error) {
	name := prefix
	if len(suffixDigestArgs) > 0 {
		suffixDigest, err := suffixDigest(suffixLength, suffixDigestArgs...)
		if err != nil {
			return "", err
		}
		name = prefix + suffixDelim + suffixDigest
	}
	if len(name) > maxLength {
		return "", fmt.Errorf("name '%s' is too long, max length is %d", name, maxLength)
	}
	return name, nil
}

func UniqueString(length int, digestArgs ...string) (string, error) {
	return suffixDigest(length, digestArgs...)
}

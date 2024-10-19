package naming

import (
	"crypto/sha256"
	"encoding/hex"
)

func HashString(strs ...string) string {
	combined := ""
	for _, s := range strs {
		combined += s
	}
	hash := sha256.Sum256([]byte(combined))
	hashedString := hex.EncodeToString(hash[:])
	return hashedString
}

func SuffixedName(prefix string, suffixDelim string, maxLength int, suffixArgs ...string) string {
	if len(suffixArgs) == 0 {
		return prefix
	}
	longName := prefix + suffixDelim + HashString(suffixArgs...)
	if len(longName) <= maxLength {
		return longName
	}
	return longName[:maxLength]
}

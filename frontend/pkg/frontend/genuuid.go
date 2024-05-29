package frontend

import (
	"encoding/base32"

	"github.com/segmentio/ksuid"
)

/*
	Pulled straight from CS code base
	This is temporary to generate CS-like ID's for clusters
*/

// uidEncoding is the lower case variant of Base32 used to encode unique identifiers.
var uidEncoding = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv")

func NewUID() string {
	return uidEncoding.EncodeToString(ksuid.New().Bytes())
}

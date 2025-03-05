package labels

import (
	. "github.com/onsi/ginkgo/v2"
)

var (
	Negative = Label("Negative")
)

// Test cases importance
var (
	Low      = Label("Low")
	Medium   = Label("Medium")
	High     = Label("High")
	Critical = Label("Critical")
)

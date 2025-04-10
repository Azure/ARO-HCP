package labels

import (
	ginkgo "github.com/onsi/ginkgo/v2"
)

var (
	Negative = ginkgo.Label("Negative")
)

// Test cases importance
var (
	Low      = ginkgo.Label("Low")
	Medium   = ginkgo.Label("Medium")
	High     = ginkgo.Label("High")
	Critical = ginkgo.Label("Critical")
)

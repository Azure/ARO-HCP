package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ARO-HCP E2E Tests")
}

var _ = BeforeSuite(func() {
	if err := setup(context.Background()); err != nil {
		panic(err)
	}
})

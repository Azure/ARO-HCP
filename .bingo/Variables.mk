# Auto generated binary variables helper managed by https://github.com/bwplotka/bingo v0.10. DO NOT EDIT.
# All tools are designed to be build inside $GOBIN.
BINGO_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GOPATH ?= $(shell go env GOPATH)
GOBIN  ?= $(firstword $(subst :, ,${GOPATH}))/bin
GO     ?= $(shell which go)

# Ensure bingo-managed tools are always built for the host platform,
# even when GOOS/GOARCH are set for cross-compilation of other targets.
GOHOSTOS     ?= $(shell $(GO) env GOHOSTOS)
GOHOSTARCH   ?= $(shell $(GO) env GOHOSTARCH)
GOHOSTARM    ?= $(shell $(GO) env GOHOSTARM)

# Below generated variables ensure that every time a tool under each variable is invoked, the correct version
# will be used; reinstalling only if needed.
# For example for addlicense variable:
#
# In your main Makefile (for non array binaries):
#
#include .bingo/Variables.mk # Assuming -dir was set to .bingo .
#
#command: $(ADDLICENSE)
#	@echo "Running addlicense"
#	@$(ADDLICENSE) <flags/args..>
#
ADDLICENSE := $(GOBIN)/addlicense-v1.1.1
$(ADDLICENSE): $(BINGO_DIR)/addlicense.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/addlicense-v1.1.1"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=addlicense.mod -o=$(GOBIN)/addlicense-v1.1.1 "github.com/google/addlicense"

APPLYCONFIGURATION_GEN := $(GOBIN)/applyconfiguration-gen-v0.34.3
$(APPLYCONFIGURATION_GEN): $(BINGO_DIR)/applyconfiguration-gen.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/applyconfiguration-gen-v0.34.3"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=applyconfiguration-gen.mod -o=$(GOBIN)/applyconfiguration-gen-v0.34.3 "k8s.io/code-generator/cmd/applyconfiguration-gen"

BINGO := $(GOBIN)/bingo-v0.10.0
$(BINGO): $(BINGO_DIR)/bingo.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/bingo-v0.10.0"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=bingo.mod -o=$(GOBIN)/bingo-v0.10.0 "github.com/bwplotka/bingo"

CLIENT_GEN := $(GOBIN)/client-gen-v0.34.3
$(CLIENT_GEN): $(BINGO_DIR)/client-gen.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/client-gen-v0.34.3"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=client-gen.mod -o=$(GOBIN)/client-gen-v0.34.3 "k8s.io/code-generator/cmd/client-gen"

GOIMPORTS := $(GOBIN)/goimports-v0.26.0
$(GOIMPORTS): $(BINGO_DIR)/goimports.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/goimports-v0.26.0"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=goimports.mod -o=$(GOBIN)/goimports-v0.26.0 "golang.org/x/tools/cmd/goimports"

GOJQ := $(GOBIN)/gojq-v0.12.17
$(GOJQ): $(BINGO_DIR)/gojq.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/gojq-v0.12.17"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=gojq.mod -o=$(GOBIN)/gojq-v0.12.17 "github.com/itchyny/gojq/cmd/gojq"

GOLANGCI_LINT := $(GOBIN)/golangci-lint-v2.5.0
$(GOLANGCI_LINT): $(BINGO_DIR)/golangci-lint.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/golangci-lint-v2.5.0"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=golangci-lint.mod -o=$(GOBIN)/golangci-lint-v2.5.0 "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"

HELM := $(GOBIN)/helm-v3.16.3
$(HELM): $(BINGO_DIR)/helm.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/helm-v3.16.3"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=helm.mod -o=$(GOBIN)/helm-v3.16.3 "helm.sh/helm/v3/cmd/helm"

INFORMER_GEN := $(GOBIN)/informer-gen-v0.34.3
$(INFORMER_GEN): $(BINGO_DIR)/informer-gen.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/informer-gen-v0.34.3"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=informer-gen.mod -o=$(GOBIN)/informer-gen-v0.34.3 "k8s.io/code-generator/cmd/informer-gen"

LISTER_GEN := $(GOBIN)/lister-gen-v0.34.3
$(LISTER_GEN): $(BINGO_DIR)/lister-gen.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/lister-gen-v0.34.3"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=lister-gen.mod -o=$(GOBIN)/lister-gen-v0.34.3 "k8s.io/code-generator/cmd/lister-gen"

MOCKGEN := $(GOBIN)/mockgen-v0.5.0
$(MOCKGEN): $(BINGO_DIR)/mockgen.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/mockgen-v0.5.0"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=mockgen.mod -o=$(GOBIN)/mockgen-v0.5.0 "go.uber.org/mock/mockgen"

ORAS := $(GOBIN)/oras-v1.2.3
$(ORAS): $(BINGO_DIR)/oras.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/oras-v1.2.3"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=oras.mod -o=$(GOBIN)/oras-v1.2.3 "oras.land/oras/cmd/oras"

YAMLFMT := $(GOBIN)/yamlfmt-v0.16.0
$(YAMLFMT): $(BINGO_DIR)/yamlfmt.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/yamlfmt-v0.16.0"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=yamlfmt.mod -o=$(GOBIN)/yamlfmt-v0.16.0 "github.com/google/yamlfmt/cmd/yamlfmt"

YQ := $(GOBIN)/yq-v4.44.5
$(YQ): $(BINGO_DIR)/yq.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/yq-v4.44.5"
	@cd $(BINGO_DIR) && GOWORK=off GOOS=$(GOHOSTOS) GOARCH=$(GOHOSTARCH) GOARM=$(GOHOSTARM) $(GO) build -mod=mod -modfile=yq.mod -o=$(GOBIN)/yq-v4.44.5 "github.com/mikefarah/yq/v4"


#!/bin/bash

# Configure this to fit your username and email
# This is done by mounting the user .gitconfig file into the container
# Command can be found in the devcontainer.json file almost at the end
# "mounts": [ ...

# This sets the git editor to the VS Code editor
git config --global core.editor "code --wait"

# Source git completions and add them to bashrc
source /usr/share/bash-completion/completions/git
echo "source /usr/share/bash-completion/completions/git" >> ~/.bashrc

# Install the typespec
# pinned to the last working version combination
npm install -g @typespec/compiler@0.51.0
npm install \
	@typespec/http@0.51.0 \
	@typespec/rest@0.51.0 \
	@typespec/versioning@0.51.0 \
	@typespec/openapi@0.51.0 \
	@typespec/openapi3@0.51.0 \
	@azure-tools/typespec-azure-core@0.37.2 \
	@azure-tools/typespec-autorest@0.37.2 \
	@azure-tools/typespec-azure-resource-manager@0.37.1

# Install azure/oav for validation of openapi and swagger example generation
# https://github.com/Azure/oav
npm install -g oav@latest

# Install the autorest used to generate golang and python clients
# it uses the dotnet, which is installed via feature in devcontainer.json
npm install -g autorest

# Install the golang-lint
# binary will be $(go env GOPATH)/bin/golangci-lint
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.55.2

golangci-lint --version

# Setup the welcome screen
echo "cat .devcontainer/motd" >> ~/.bashrc
echo "cat .devcontainer/motd" >> ~/.zshrc

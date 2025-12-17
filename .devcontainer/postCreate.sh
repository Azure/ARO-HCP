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

cd api && npm ci --legacy-peer-deps

# Setup the welcome screen
echo "cat .devcontainer/motd" >> ~/.bashrc
echo "cat .devcontainer/motd" >> ~/.zshrc

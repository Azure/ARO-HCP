# ARO-HCP

# Description
The RP for the ARO-HCP project.

## Development setup

For instructions on building out a dev environment for testing -- Review the [Dev Infrastructure](./dev-infrastructure/docs/development-setup.md) guide

For instructions on building and testing Frontend -- Check out Frontend's [README](./frontend/README.md)

## Remote Containers Development setup

The setup is based on VSCode Remote Containers. See [here](https://code.visualstudio.com/docs/remote/containers) for more information.

VSCode should be installed from the [offical downloads page](https://code.visualstudio.com/download) (as opposed to other sources, like flatpak). This is to avoid potential docker compatibility issues with the required extensions mentioned below.

The predefined container is in `.devcontainer` with a custom `postCreate.sh`.
To use it, please install the [Remote - Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) extension in VSCode.

The VSCode will have the following extensions installed:
- [golang.go](https://marketplace.visualstudio.com/items?itemName=golang.Go)
- [editorconfig.editorconfig](https://marketplace.visualstudio.com/items?itemName=EditorConfig.EditorConfig)
- [ms-azuretools.vscode-bicep](https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-bicep)
- [ms-vscode.azurecli](https://marketplace.visualstudio.com/items?itemName=ms-vscode.azurecli)
- [arjun.swagger-viewer](https://marketplace.visualstudio.com/items?itemName=Arjun.swagger-viewer)

During the container setup, it also installs golangci-lint, which is the de facto standard for linting go.

On top of that, it sets up the Bicep CLI and the Azure CLI with the Bicep extension
to simplify the development of infra code.

Finally, the container also contains the nodejs and sets up the typespec which is needed for the ARM contract development, as it is now mandatory to have the typespec in the ARM templates.
To enable the typespec extensions, which is not yet part of official extensions, once the vscode opens and the devcontainer is ready, you need to run the following command
```bash
tsp code install
```

If you are developing on MacOS you will need to install both docker cli (NOT docker desktop) and colima. There have been issues with the devcontainer working with vscode using podman desktop.

```bash
brew install docker
brew install colima
```

Before running your devcontainer, make sure colima is started.
```bash
colima start --cpu 4 --memory 8 --vz-rosetta --vm-type=vz
```

Then, rebuild and connect to the dev container: `cmd + shift + P` => `dev containers: rebuild container`

**Most importantly**, the container is set up to use the same user as the host machine, so you can use the same git config and ssh keys.
It is implemented as a host mount in the `.devcontainer/devcontainer.json` file.

```json
"mounts": [
    "source=${localEnv:HOME}/.gitconfig,target=/home/vscode/.gitconfig,type=bind,consistency=cached"
],
```

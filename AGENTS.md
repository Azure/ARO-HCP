This project is the main repo for Red Hat OpenShift on Azure (ARO) in the Hosted Control Planes architecture.

It has a multi-layered build and deploy system because it supports multiple target environments with different deployment solutions:
- personal dev envs can be set up with `make` and a properly-configured `az` command
- shared/integrated dev envs use github actions
- production systems use Microsoft ADO and EV2
More info:
- [Tenants and Environments](docs/environments.md)
- [Deployment via EV2](docs/ev2-deployment.md)
- [Setting up a Personal DEV Env](docs/personal-dev.md)

Related projects:
- There's also a helper repo at github.com/Azure/ARO-Tools you can propose changes to

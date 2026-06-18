# Make Options

## Docker vs Podman

By default, all `make` targets working with containers or container images will use **podman** if it is installed - and fall back to **Docker** if not.

You can force the usage of Docker with `make CONTAINER_ENGINE=docker ...`.

## Parallel builds
By default, all `make build-services` (and all `make` targets depending on it) will trigger up to 7 jobs in parallel to build the sub components.

You can change this behavior by overriding `BUILD_SERVICES_OPTS` and proving an alternative `-j` parameter for make, e.g.

* `make BUILD_SERVICES_OPTS="-j1" ...` will limit number of parallel jobs to 1
* `make BUILD_SERVICES_OPTS="-j" ...` will not limit number of parallel jobs
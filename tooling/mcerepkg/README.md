# ACM Multicluster Engine RePackage

This tool repackages an MCE OLM release bundle as a Helm chart.

## Approach

- extract manifests from OLM bundle
- sanity check the rough structure of the manifests (exepcted artifacts, expected ENV vars, ...)
- templatize namspace and image references

## Find OLM bundle image

The OLM release bundle image for an MCE release can be found on <https://catalog.redhat.com/software/containers/multicluster-engine/mce-operator-bundle/6160406290fb938ecf6009c6>. The image ref can also be constructed as `registry.redhat.io/multicluster-engine/mce-operator-bundle:v$(version)`.

This image needs to be pulled and saved to a tgz file

```sh
podman pull --arch x86_64 $BUNDLE_IMAGE
podman save -o mce-bundle.tgz $BUNDLE_IMAGE
```

## Generate the helm chart

```sh
go run . \
   -b mce-bundle.tgz \
   -l $BUNDLE_IMAGE \
   -o helm -s ../../acm/scaffold
```

## Next steps

1. Overwrite the old helm chart files with the new ones (make sure not to leave around deleted ones).
2. Run `make all-tidy` in repo root, which is likely to modify indenting in the helm files.

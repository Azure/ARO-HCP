Docs on how these configs work:
- docs/configuration.md
- docs/bicep.md if you're editing bicep files

When making changes to `configs/config*.yaml` you might need to update the appropriate schema files.

After updating configs, verify and render new config by running:
- `cd dev-infrastructure; ./create-config.sh dev`
- `cd config; make materialize`
These might result in changes to rendered configs which must be attached to any PRs.

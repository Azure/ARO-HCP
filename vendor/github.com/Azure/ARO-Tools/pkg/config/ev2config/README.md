# Ev2 Central Configuration

We want to provide access to the Ev2 central configuration in `templatize` local runs using pipeline configurations.
Furthermore, we want to do this without requiring that users PIM to higher levels of privilege required to access the
true configuration using the `ev2` CLI, so we can store a copy of the configuration in this repo and use it as necessary.
However, we want to make sure that we are not accidentally leaking anything important from this configuration, so we
'sanitize' the inputs to strip out just the fields we need.

## Accessing Central Configuration

It is challenging to automate the download of central configuration files from Ev2. While the `ev2` CLI does work for
public cloud values, an escort and SAW would be required to use it for sovereign clouds. Use the [portal](https://ev2portal.azure.net/#config/)
to access the values instead and populate `public.config.json` and `ff.config.json` before sanitizing them.
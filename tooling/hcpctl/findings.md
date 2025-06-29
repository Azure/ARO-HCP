# Review findings

- are all tests in cmd/breakglass/base/options.go safe to run in parallel? becaues of the env var modification
- is what we do with cmd/breakglass/hcp/format.go the most simple clean and golang native way to format errors in cli apps? investigate and make a best practice proposal
- using RawHCPOptions for the hcp list cluster subcommand is not ideal. none of the parameters in there make sense, and same for using RawMCOptions for the mc list cluster subcommand.

# Microsoft Authentication Library (MSAL) Extensions for Go

This module contains a persistent cache for [Microsoft Authentication Library (MSAL) for Go](https://github.com/AzureAD/microsoft-authentication-library-for-go) public client applications such as CLI tools. It isn't recommended for web applications or RPC APIs, in which it can cause scaling and performance problems.

The cache supports encrypted storage on Linux, macOS and Windows. The encryption facility depends on the platform:
- Linux: [libsecret](https://wiki.gnome.org/Projects/Libsecret) (used as a DBus Secret Service client)
- macOS: keychain
- Windows: data protection API (DPAPI)

See the `accessor` package for more details. The `file` package has a plaintext storage provider to use when encryption isn't possible.

> Plaintext storage is dangerous. Bearer tokens are not cryptographically bound to a machine and can be stolen. In particular, the refresh token can be used to get access tokens for many resources.
> It's important to warn end-users before falling back to plaintext. End-users should ensure they store the tokens in a secure location (e.g. encrypted disk) and must understand they are responsible for their safety.

## Installation

```sh
go get -u github.com/AzureAD/microsoft-authentication-extensions-for-go/cache
```

## Contributing

This project welcomes contributions and suggestions.  Most contributions require you to agree to a
Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us
the rights to use your contribution. For details, visit https://cla.opensource.microsoft.com.

When you submit a pull request, a CLA bot will automatically determine whether you need to provide
a CLA and decorate the PR appropriately (e.g., status check, comment). Simply follow the instructions
provided by the bot. You will only need to do this once across all repos using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or
contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

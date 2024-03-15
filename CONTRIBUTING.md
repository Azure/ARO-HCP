# Contributing to ARO HCP

Welcome to the ARO HCP project! We appreciate your interest in contributing. This guide will help you get started with the contribution process.


## Table of Contents
- [Getting Started](#getting-started)
- [Contributing Guidelines](#contributing-guidelines)
- [Code of Conduct](#code-of-conduct)
- [License](#license)


## Getting Started
To contribute to ARO HCP, follow these steps:

1. Fork the repository to your GitHub account.
2. Clone the forked repository to your local machine.
3. Create a new branch for your changes.
4. Make your changes and commit them.
5. Push your changes to your forked repository.
6. Submit a pull request to the main repository.


## Contributing Guidelines
Please follow these guidelines when contributing to ARO HCP:

- Please consider, starting with a draft PR, unless you are ready for review. If you want a early feedback,
  do not hesitate to ping the code owners.
- Write meaningful commit messages and PR description. The PR will be squashed before merging, unless
  the splitting into multiple commits is explicitly needed in order to separate changes and allow
  later `git bisect`.
- The repository is structured according to the focus areas, e.g. `api` containing all exposed API specs.
  When you contribute, please follow this structure and add your contribution to the appropriate folder.
  When in doubt, open PR early and ask for feedback.
- When applicable, please always cover new functionality with the appropriate tests.
- When adding functionality, that is not yet implemented, please write appropriate documentation.
  When in doubt, ask yourself what it took you to understand the functionality, and what would you need
  to know to use it.
- When adding new features, please consider to record a short video showing how it works and explaining
  the use case. This will help others to understand better even before digging into the code. When done,
  upload the recording to the [Drive](https://drive.google.com/drive/folders/1RB1L2-nGMXwsOAOYC-VGGbB0yD3Ae-rD?usp=drive_link) and share the link in the PR.
- When you are working on the issue that has Jira card, please always document all tradeoffs and decisions
  in the Jira card. Please, also include all design documents and slack discussion in the Jira. This will
  help others to understand the context and decisions made.

Please note, that you might be asked to comply with these guidelines before your PR is accepted.


## Code of Conduct
This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.


## License
ARO HCP is licensed under the Apache License, Version 2.0. Please see the [LICENSE](LICENSE) file for more details.

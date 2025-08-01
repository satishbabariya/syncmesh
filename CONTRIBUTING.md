# Contributing to SyncMesh

We love your input! We want to make contributing to SyncMesh as easy and transparent as possible, whether it's:

- Reporting a bug
- Discussing the current state of the code
- Submitting a fix
- Proposing new features
- Becoming a maintainer

## Development Process

We use GitHub to host code, track issues and feature requests, as well as accept pull requests.

## Pull Requests

Pull requests are the best way to propose changes to the codebase. We actively welcome your pull requests:

1. Fork the repo and create your branch from `main`.
2. If you've added code that should be tested, add tests.
3. If you've changed APIs, update the documentation.
4. Ensure the test suite passes.
5. Make sure your code lints.
6. Issue that pull request!

## Development Setup

1. **Clone the repository**:
   ```bash
   git clone https://github.com/satishbabariya/syncmesh.git
   cd syncmesh
   ```

2. **Install dependencies**:
   ```bash
   go mod download
   ```

3. **Run tests**:
   ```bash
   make test
   ```

4. **Build the project**:
   ```bash
   make build
   ```

5. **Start development environment**:
   ```bash
   make up
   ```

## Code Style

- Use `gofmt` to format your code
- Run `golangci-lint` for linting
- Follow Go naming conventions
- Write meaningful commit messages
- Add comments for complex logic

## Testing

- Write unit tests for new functionality
- Ensure all tests pass before submitting PR
- Add integration tests when appropriate
- Test with different configurations

## Bug Reports

We use GitHub issues to track public bugs. Report a bug by opening a new issue.

**Great Bug Reports** tend to have:

- A quick summary and/or background
- Steps to reproduce
  - Be specific!
  - Give sample code if you can
- What you expected would happen
- What actually happens
- Notes (possibly including why you think this might be happening, or stuff you tried that didn't work)

## License

By contributing, you agree that your contributions will be licensed under the MIT License.

## References

This document was adapted from the open-source contribution guidelines for [Facebook's Draft](https://github.com/facebook/draft-js/blob/a9316a723f9e918afde44dea68b5f9f39b7d9b00/CONTRIBUTING.md)
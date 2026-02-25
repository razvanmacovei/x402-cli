# Contributing to x402-cli

Thank you for your interest in contributing! This guide will help you get started.

## Getting Started

### Prerequisites

- Go 1.25+

### Development Setup

```bash
# Clone the repository
git clone https://github.com/razvanmacovei/x402-cli.git
cd x402-cli

# Install dependencies
go mod download

# Build
make build

# Run tests
go test ./...
```

## How to Contribute

### Reporting Issues

- Use [GitHub Issues](https://github.com/razvanmacovei/x402-cli/issues) for bug reports and feature requests
- Search existing issues before creating a new one
- For security vulnerabilities, see [SECURITY.md](SECURITY.md)

### Pull Requests

1. Fork the repository
2. Create a feature branch from `main`: `git checkout -b feature/my-feature`
3. Make your changes
4. Run tests: `go test ./...`
5. Ensure the build passes: `make build`
6. Commit with clear, descriptive messages
7. Push to your fork and open a Pull Request

### PR Guidelines

- Keep PRs focused on a single change
- Add tests for new functionality
- Update documentation if needed
- Follow existing code style and patterns
- Ensure all CI checks pass

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).

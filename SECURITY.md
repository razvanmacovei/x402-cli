# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.x.x   | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability in x402-cli, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please send a report to:

- GitHub Security Advisory: [Report a vulnerability](https://github.com/razvanmacovei/x402-cli/security/advisories/new)

### What to include

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### Response timeline

- **Acknowledgment**: Within 48 hours
- **Initial assessment**: Within 1 week
- **Fix and disclosure**: Coordinated with reporter

## Security Considerations

This CLI handles EVM payment signing. Key security considerations:

- `EVM_PRIVATE_KEY` is read from the environment and used only for EIP-3009 signing in-process
- The private key is never transmitted to remote endpoints or logged
- Payment signatures are sent only to the target x402 server, which forwards to the facilitator
- Use a dedicated low-value wallet, not your main account
- All network communication uses HTTPS (TLS verification can be disabled with `-k` for local development only)

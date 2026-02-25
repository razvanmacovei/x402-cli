# Changelog

All notable changes to this project will be documented in this file.

## [0.5.4] - 2026-02-25

### Added

- OpenClaw skill section in README with ClawHub install instructions
- SKILL.md frontmatter declaring required env vars, binaries, and install methods
- Code of Conduct, Contributing guide, and Security policy

### Changed

- Moved ClawHub skill files to `skill/` subfolder (only SKILL.md + clawhub.json published)
- Removed incorrect Solana network claim from clawhub.json (EVM-only)
- Added private key security guidance to clawhub.json

### Security

- Bumped `golang.org/x/crypto` from 0.41.0 to 0.45.0 (SSH DoS fix, agent panic fix)
- Bumped `go-ethereum` from 1.16.7 to 1.17.0 (CVE-2026-26313, CVE-2026-26314, CVE-2026-26315)
- Bumped `gnark-crypto` from 0.18.0 to 0.18.1 (memory allocation fix)

## [0.5.3] - 2026-02-25

### Added

- ClawHub publish step in release workflow (later removed in favor of manual publish)
- OpenClaw skill section in README

## [0.5.2] - 2026-02-25

### Fixed

- Show "unknown command" for non-URL arguments

## [0.5.1] - 2026-02-25

### Fixed

- Validate URL before making HTTP request

## [0.5.0] - 2026-02-25

### Added

- Version and help subcommands
- Wallet subcommand and `-o` output flag
- ClawHub skill manifest for OpenClaw agent integration

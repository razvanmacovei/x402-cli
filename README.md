# x402-cli

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/razvanmacovei/x402-cli)](https://github.com/razvanmacovei/x402-cli/releases)

**CLI tool for [x402](https://x402.org) payment-gated endpoints. Built for humans and AI agents.**

Sends two requests to any x402-enabled API:
1. Without payment — expects `402 Payment Required`, decodes and displays the payment requirements
2. With payment — signs a USDC payment using the [Coinbase x402 Go SDK](https://github.com/coinbase/x402), displays the settlement result

Works with any x402-compliant server, including the [x402-k8s-operator](https://github.com/razvanmacovei/x402-k8s-operator).

## Install

**Homebrew:**
```bash
brew install razvanmacovei/tap/x402-cli
```

**Go install:**
```bash
go install github.com/razvanmacovei/x402-cli@latest
```

**From releases:**
```bash
# macOS (Apple Silicon)
curl -sL https://github.com/razvanmacovei/x402-cli/releases/latest/download/x402-cli_darwin_arm64.tar.gz | tar xz
sudo mv x402-cli /usr/local/bin/

# Linux (amd64)
curl -sL https://github.com/razvanmacovei/x402-cli/releases/latest/download/x402-cli_linux_amd64.tar.gz | tar xz
sudo mv x402-cli /usr/local/bin/
```

**Build from source:**
```bash
git clone https://github.com/razvanmacovei/x402-cli.git
cd x402-cli
make build
```

## Usage

```bash
# Test a paid endpoint (requires wallet with USDC on the endpoint's network)
export EVM_PRIVATE_KEY=0x...
x402-cli https://api.example.com/paid-endpoint

# Only check payment requirements (Step 1, no payment sent)
x402-cli --skip-verify https://api.example.com/paid-endpoint

# Self-signed TLS (local development)
x402-cli -k https://podinfo.localhost/api/info

# POST with JSON body and custom headers
x402-cli -X POST -d '{"query": "hello"}' -H 'Content-Type: application/json' https://api.example.com/ask

# Verbose output (show full request/response headers)
x402-cli -v https://api.example.com/paid-endpoint

# Dry-run: show cost and ask for confirmation before paying
x402-cli --dry-run https://api.example.com/paid-endpoint

# Agent mode: JSON output, auto-confirm payment, no interactive prompts
export EVM_PRIVATE_KEY=0x...
x402-cli --json -y https://api.example.com/paid-endpoint

# Agent probe: check price without paying
x402-cli --json --skip-verify https://api.example.com/paid-endpoint
```

### Flags

| Flag | Description |
|------|-------------|
| `-k`, `--insecure` | Skip TLS certificate verification |
| `-X`, `--method` | HTTP method (default: `GET`, `POST` if `-d` is set) |
| `-d`, `--data` | Request body (implies `POST` if `-X` not set) |
| `-H`, `--header` | Custom header `Key: Value` (repeatable) |
| `-v`, `--verbose` | Show full request/response headers |
| `--dry-run` | Show payment cost and ask for confirmation before paying |
| `--json` | Output structured JSON (for agents and scripts) |
| `-y`, `--yes` | Auto-confirm payment without prompting |
| `-q`, `--quiet` | Suppress human-readable output |
| `--timeout` | Request timeout (default: `30s`) |
| `--skip-verify` | Only run Step 1 (no payment) |
| `--version` | Print version |

### Environment

| Variable | Description |
|----------|-------------|
| `EVM_PRIVATE_KEY` | Private key for signing payments (required for Step 2) |

## Example Output

```
x402-cli v0.1.0
Endpoint: https://podinfo.localhost/api/info
Method:   GET

--- Step 1: Request without payment ---
Status: 402
PAYMENT-REQUIRED: eyJ4NDAyVmVyc2lvbiI6Miwic...
PAYMENT-REQUIRED (decoded):
  {
    "x402Version": 2,
    "resource": {
      "url": "/api/info",
      "description": "Payment required to access this resource"
    },
    "accepts": [
      {
        "scheme": "exact",
        "network": "eip155:84532",
        "amount": "1000",
        "payTo": "0x4DEF22cad784C9fd21272e545F349Fea83BF8764",
        "maxTimeoutSeconds": 300,
        "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
        "extra": { "name": "USDC", "version": "2" }
      }
    ]
  }
Body: {"x402Version":2,...}

--- Step 2: Request with x402 payment ---
Signer: 0xe312C88D2102f1eE75aBD221cA9dc72Db406ae4A
Status: 200
PAYMENT-RESPONSE: eyJzdWNjZXNzIjp0cnVlLC...
PAYMENT-RESPONSE (decoded):
  {
    "success": true,
    "payer": "0xe312C88D2102f1eE75aBD221cA9dc72Db406ae4A",
    "transaction": "0xacf95894d5ff06e29cacf6fe2c9c87c2f4ea987528b9ebf138a0ab4755082325",
    "network": "eip155:84532"
  }
Body: {
  "hostname": "podinfo-6d8c9495d6-gl4zx",
  "version": "6.10.1",
  "message": "greetings from podinfo v6.10.1",
  ...
}

Payment accepted!
```

## How It Works

```
x402-cli                    x402 Server               Facilitator
      |                                  |                         |
      |--- GET /api/info --------------->|                         |
      |<-- 402 + PAYMENT-REQUIRED -------|                         |
      |                                  |                         |
      | (decode requirements,            |                         |
      |  sign EIP-3009 authorization)    |                         |
      |                                  |                         |
      |--- GET /api/info + Payment-Signature -->|                  |
      |                                  |--- POST /verify ------->|
      |                                  |<-- {isValid: true} -----|
      |                                  |--- POST /settle ------->|
      |                                  |<-- {success, tx} -------|
      |<-- 200 + PAYMENT-RESPONSE -------|                         |
```

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success (payment accepted or probe completed) |
| `1` | Error (network, config, or unexpected failure) |
| `2` | Payment rejected by facilitator |
| `3` | Route is free (no payment needed) |

## Agent Integration

The `--json` flag outputs structured JSON that agents can parse directly:

```bash
# Probe endpoint and parse with jq
RESULT=$(x402-cli --json --skip-verify https://api.example.com/endpoint)
PRICE=$(echo "$RESULT" | jq -r '.probe.paymentRequirements.accepts[0].amount')
NETWORK=$(echo "$RESULT" | jq -r '.probe.paymentRequirements.accepts[0].network')

# Pay and get response
RESULT=$(x402-cli --json -y https://api.example.com/endpoint)
STATUS=$(echo "$RESULT" | jq -r '.status')        # "accepted", "rejected", "free", "error"
BODY=$(echo "$RESULT" | jq -r '.payment.body')     # backend response
TX=$(echo "$RESULT" | jq -r '.payment.paymentResponse.transaction')
```

JSON output fields:
- `status`: `"free"`, `"payment_required"`, `"accepted"`, `"rejected"`, `"error"`
- `probe.paymentRequired`: boolean
- `probe.paymentRequirements`: decoded x402 payment requirements
- `payment.accepted`: boolean
- `payment.paymentResponse`: decoded facilitator settle response (includes `transaction` hash)
- `error`: error message (when `status` is `"error"`)

## Supported Networks

Any EVM network supported by the x402 protocol:

| Network | Chain ID |
|---------|----------|
| Base | eip155:8453 |
| Base Sepolia | eip155:84532 |
| Avalanche | eip155:43114 |
| Avalanche Fuji | eip155:43113 |

## Related

- [x402-k8s-operator](https://github.com/razvanmacovei/x402-k8s-operator) — Kubernetes operator that monetizes any API with x402
- [x402 protocol](https://x402.org) — HTTP 402 payment protocol by Coinbase
- [x402 Go SDK](https://github.com/coinbase/x402/tree/main/go) — Official Go client library

## License

[Apache License 2.0](LICENSE)

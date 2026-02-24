package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	x402 "github.com/coinbase/x402/go"
	x402http "github.com/coinbase/x402/go/http"
	evm "github.com/coinbase/x402/go/mechanisms/evm/exact/client"
	evmsigners "github.com/coinbase/x402/go/signers/evm"
)

var version string

func main() {
	var (
		insecure   bool
		timeout    time.Duration
		method     string
		showVer    bool
		skipVerify bool
	)

	if version == "" {
		version = buildVersion()
	}

	flag.BoolVar(&insecure, "insecure", false, "Skip TLS certificate verification")
	flag.BoolVar(&insecure, "k", false, "Skip TLS certificate verification (shorthand)")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "Request timeout")
	flag.StringVar(&method, "method", "GET", "HTTP method")
	flag.StringVar(&method, "X", "GET", "HTTP method (shorthand)")
	flag.BoolVar(&showVer, "version", false, "Print version and exit")
	flag.BoolVar(&skipVerify, "skip-verify", false, "Only send Step 1 (no payment), skip Step 2")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "x402-cli %s — test x402 payment endpoints\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage:\n  x402-cli [flags] <url>\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  EVM_PRIVATE_KEY    Private key for signing payments (required for Step 2)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if showVer {
		fmt.Printf("x402-cli %s\n", version)
		os.Exit(0)
	}

	endpoint := flag.Arg(0)
	if endpoint == "" {
		fmt.Fprintln(os.Stderr, "Error: URL argument is required")
		fmt.Fprintln(os.Stderr, "Usage: x402-cli [flags] <url>")
		os.Exit(1)
	}

	privateKey := os.Getenv("EVM_PRIVATE_KEY")

	transport := &http.Transport{}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	fmt.Printf("x402-cli %s\n", version)
	fmt.Printf("Endpoint: %s\n", endpoint)
	fmt.Printf("Method:   %s\n\n", method)

	// --- Step 1: Request without payment → expect 402 ---
	fmt.Println("--- Step 1: Request without payment ---")

	plainClient := &http.Client{Transport: transport, Timeout: timeout}
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}

	resp, err := plainClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	fmt.Printf("Status: %d\n", resp.StatusCode)
	if payReq := resp.Header.Get("PAYMENT-REQUIRED"); payReq != "" {
		printBase64Header("PAYMENT-REQUIRED", payReq)
	}
	fmt.Printf("Body: %s\n\n", truncate(string(body), 300))

	if resp.StatusCode != http.StatusPaymentRequired {
		fmt.Println("Endpoint did not return 402 Payment Required.")
		if resp.StatusCode == http.StatusOK {
			fmt.Println("The endpoint is accessible without payment (free route).")
		}
		os.Exit(0)
	}

	if skipVerify {
		fmt.Println("--skip-verify: stopping after Step 1.")
		os.Exit(0)
	}

	// --- Step 2: Request with x402 payment ---
	if privateKey == "" {
		fmt.Fprintln(os.Stderr, "\nError: EVM_PRIVATE_KEY is required for Step 2 (payment).")
		fmt.Fprintln(os.Stderr, "Set it with: export EVM_PRIVATE_KEY=0x...")
		os.Exit(1)
	}

	fmt.Println("--- Step 2: Request with x402 payment ---")

	evmSigner, err := evmsigners.NewClientSignerFromPrivateKey(privateKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create signer: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Signer: %s\n", evmSigner.Address())

	x402Client := x402.Newx402Client().
		Register("eip155:*", evm.NewExactEvmScheme(evmSigner))

	httpClient := x402http.WrapHTTPClientWithPayment(
		&http.Client{Transport: transport, Timeout: timeout},
		x402http.Newx402HTTPClient(x402Client),
	)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req2, _ := http.NewRequestWithContext(ctx, method, endpoint, nil)
	resp2, err := httpClient.Do(req2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Payment request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)
	fmt.Printf("Status: %d\n", resp2.StatusCode)

	if payResp := resp2.Header.Get("PAYMENT-RESPONSE"); payResp != "" {
		printBase64Header("PAYMENT-RESPONSE", payResp)
	}
	fmt.Printf("Body: %s\n\n", truncate(string(body2), 500))

	switch resp2.StatusCode {
	case http.StatusOK:
		fmt.Println("Payment accepted!")
	case http.StatusPaymentRequired:
		fmt.Println("Payment was rejected. Check wallet balance and facilitator logs.")
		os.Exit(1)
	default:
		fmt.Printf("Unexpected status %d.\n", resp2.StatusCode)
		os.Exit(1)
	}
}

func buildVersion() string {
	// Overridden by -ldflags at build time. If not set, try go module info
	// (works with `go install ...@v0.1.0`).
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "(devel)" && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

func printBase64Header(name, value string) {
	fmt.Printf("%s: %s...\n", name, truncate(value, 60))
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		var pretty json.RawMessage
		if json.Unmarshal(decoded, &pretty) == nil {
			indented, _ := json.MarshalIndent(pretty, "  ", "  ")
			fmt.Printf("%s (decoded):\n  %s\n", name, string(indented))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

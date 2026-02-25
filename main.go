package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"
	"time"

	x402 "github.com/coinbase/x402/go"
	x402http "github.com/coinbase/x402/go/http"
	evm "github.com/coinbase/x402/go/mechanisms/evm/exact/client"
	evmsigners "github.com/coinbase/x402/go/signers/evm"
)

var version string

// headerFlags collects multiple -H flags.
type headerFlags []string

func (h *headerFlags) String() string { return strings.Join(*h, ", ") }
func (h *headerFlags) Set(val string) error {
	*h = append(*h, val)
	return nil
}

func main() {
	var (
		insecure   bool
		timeout    time.Duration
		method     string
		showVer    bool
		skipVerify bool
		data       string
		verbose    bool
		dryRun     bool
		headers    headerFlags
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
	flag.StringVar(&data, "data", "", "Request body (implies POST if -X not set)")
	flag.StringVar(&data, "d", "", "Request body (shorthand)")
	flag.Var(&headers, "H", "Custom header 'Key: Value' (repeatable)")
	flag.Var(&headers, "header", "Custom header 'Key: Value' (repeatable)")
	flag.BoolVar(&verbose, "verbose", false, "Show full request/response headers")
	flag.BoolVar(&verbose, "v", false, "Show full request/response headers (shorthand)")
	flag.BoolVar(&dryRun, "dry-run", false, "Show payment cost and ask for confirmation before paying")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "x402-cli %s — test x402 payment endpoints\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage:\n  x402-cli [flags] <url>\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  x402-cli https://api.example.com/paid-endpoint\n")
		fmt.Fprintf(os.Stderr, "  x402-cli -k https://podinfo.localhost/api/info\n")
		fmt.Fprintf(os.Stderr, "  x402-cli -X POST -d '{\"query\": \"hello\"}' -H 'Content-Type: application/json' https://api.example.com/ask\n")
		fmt.Fprintf(os.Stderr, "  x402-cli -v --dry-run https://api.example.com/paid-endpoint\n\n")
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

	// If -d is set and method was not explicitly changed, default to POST.
	if data != "" && method == "GET" {
		method = "POST"
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
	req, err := newRequest(method, endpoint, data, headers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}

	if verbose {
		dumpRequest(req)
	}

	resp, err := plainClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if verbose {
		dumpResponse(resp, body)
	}

	fmt.Printf("Status: %d\n", resp.StatusCode)
	if payReq := resp.Header.Get("PAYMENT-REQUIRED"); payReq != "" {
		printBase64Header("PAYMENT-REQUIRED", payReq)
	}
	if !verbose {
		fmt.Printf("Body: %s\n\n", truncate(string(body), 300))
	}

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

	// --- Dry-run: show cost and confirm ---
	if dryRun {
		printPaymentSummary(resp, body)
		fmt.Print("\nProceed with payment? [y/N] ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(scanner.Text())), "y") {
			fmt.Println("Aborted.")
			os.Exit(0)
		}
		fmt.Println()
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

	req2, _ := newRequestWithContext(ctx, method, endpoint, data, headers)
	resp2, err := httpClient.Do(req2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Payment request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)

	if verbose {
		dumpResponse(resp2, body2)
	}

	fmt.Printf("Status: %d\n", resp2.StatusCode)

	if payResp := resp2.Header.Get("PAYMENT-RESPONSE"); payResp != "" {
		printBase64Header("PAYMENT-RESPONSE", payResp)
	}
	if !verbose {
		fmt.Printf("Body: %s\n\n", truncate(string(body2), 500))
	}

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

// newRequest creates an HTTP request with optional body and custom headers.
func newRequest(method, url, data string, headers headerFlags) (*http.Request, error) {
	var bodyReader io.Reader
	if data != "" {
		bodyReader = strings.NewReader(data)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, headers)
	return req, nil
}

// newRequestWithContext creates an HTTP request with context, optional body and custom headers.
func newRequestWithContext(ctx context.Context, method, url, data string, headers headerFlags) (*http.Request, error) {
	var bodyReader io.Reader
	if data != "" {
		bodyReader = strings.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, headers)
	return req, nil
}

// applyHeaders parses "Key: Value" strings and sets them on the request.
func applyHeaders(req *http.Request, headers headerFlags) {
	for _, h := range headers {
		if k, v, ok := strings.Cut(h, ":"); ok {
			req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
}

// dumpRequest prints the full HTTP request in verbose mode.
func dumpRequest(req *http.Request) {
	dump, err := httputil.DumpRequestOut(req, true)
	if err != nil {
		return
	}
	fmt.Printf("→ Request:\n%s\n", string(dump))
}

// dumpResponse prints the full HTTP response in verbose mode.
func dumpResponse(resp *http.Response, body []byte) {
	fmt.Printf("← Response:\n%s %s\n", resp.Proto, resp.Status)
	for k, vals := range resp.Header {
		for _, v := range vals {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
	fmt.Printf("\n%s\n\n", string(body))
}

// printPaymentSummary extracts and displays the cost from a 402 response.
func printPaymentSummary(resp *http.Response, body []byte) {
	fmt.Println("\n--- Payment Summary ---")

	var payInfo struct {
		Accepts []struct {
			Amount  string `json:"amount"`
			Asset   string `json:"asset"`
			Network string `json:"network"`
			PayTo   string `json:"payTo"`
			Extra   struct {
				Name string `json:"name"`
			} `json:"extra"`
		} `json:"accepts"`
		Resource struct {
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"resource"`
	}

	if err := json.Unmarshal(body, &payInfo); err == nil {
		if payInfo.Resource.URL != "" {
			fmt.Printf("Resource: %s\n", payInfo.Resource.URL)
		}
		for _, a := range payInfo.Accepts {
			assetName := a.Extra.Name
			if assetName == "" {
				assetName = a.Asset
			}
			fmt.Printf("Cost:     %s %s (atomic units)\n", a.Amount, assetName)
			fmt.Printf("Network:  %s\n", a.Network)
			fmt.Printf("Pay to:   %s\n", a.PayTo)
		}
	}
}

func buildVersion() string {
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

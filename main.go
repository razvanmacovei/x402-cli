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

// Exit codes for programmatic use by agents.
const (
	ExitSuccess         = 0
	ExitError           = 1
	ExitPaymentRejected = 2
	ExitFreeRoute       = 3
)

// headerFlags collects multiple -H flags.
type headerFlags []string

func (h *headerFlags) String() string { return strings.Join(*h, ", ") }
func (h *headerFlags) Set(val string) error {
	*h = append(*h, val)
	return nil
}

// jsonResult is the structured output for --json mode.
type jsonResult struct {
	Version  string       `json:"version"`
	Endpoint string       `json:"endpoint"`
	Method   string       `json:"method"`
	Status   string       `json:"status"`
	Probe    *probeResult `json:"probe"`
	Payment  *payResult   `json:"payment,omitempty"`
	Error    string       `json:"error,omitempty"`
}

type probeResult struct {
	StatusCode          int              `json:"statusCode"`
	PaymentRequired     bool             `json:"paymentRequired"`
	PaymentRequirements *json.RawMessage `json:"paymentRequirements,omitempty"`
	Body                string           `json:"body,omitempty"`
}

type payResult struct {
	StatusCode      int              `json:"statusCode"`
	Accepted        bool             `json:"accepted"`
	Signer          string           `json:"signer,omitempty"`
	PaymentResponse *json.RawMessage `json:"paymentResponse,omitempty"`
	Body            string           `json:"body,omitempty"`
}

func main() {
	if version == "" {
		version = buildVersion()
	}

	// Handle "wallet" subcommand before flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "wallet" {
		runWalletCmd(os.Args[2:])
		return
	}

	var (
		insecure   bool
		timeout    time.Duration
		method     string
		showVer    bool
		skipVerify bool
		data       string
		verbose    bool
		dryRun     bool
		jsonOutput bool
		autoYes    bool
		quiet      bool
		outputFile string
		headers    headerFlags
	)

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
	flag.BoolVar(&jsonOutput, "json", false, "Output structured JSON (for agents and scripts)")
	flag.BoolVar(&autoYes, "yes", false, "Auto-confirm payment without prompting")
	flag.BoolVar(&autoYes, "y", false, "Auto-confirm payment without prompting (shorthand)")
	flag.BoolVar(&quiet, "quiet", false, "Suppress human-readable output, only print JSON or exit code")
	flag.BoolVar(&quiet, "q", false, "Suppress human-readable output (shorthand)")
	flag.StringVar(&outputFile, "output", "", "Save response body to file")
	flag.StringVar(&outputFile, "o", "", "Save response body to file (shorthand)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "x402-cli %s — test x402 payment endpoints\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage:\n  x402-cli [flags] <url>\n  x402-cli wallet [--network <name>] [--json]\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  x402-cli https://api.example.com/paid-endpoint\n")
		fmt.Fprintf(os.Stderr, "  x402-cli -k https://podinfo.localhost/api/info\n")
		fmt.Fprintf(os.Stderr, "  x402-cli -X POST -d '{\"query\": \"hello\"}' -H 'Content-Type: application/json' https://api.example.com/ask\n")
		fmt.Fprintf(os.Stderr, "  x402-cli -v --dry-run https://api.example.com/paid-endpoint\n")
		fmt.Fprintf(os.Stderr, "  x402-cli --json -y -o response.json https://api.example.com/paid-endpoint\n")
		fmt.Fprintf(os.Stderr, "  x402-cli wallet                          # show address + USDC balances\n")
		fmt.Fprintf(os.Stderr, "  x402-cli wallet --network base-sepolia   # single network\n\n")
		fmt.Fprintf(os.Stderr, "Exit codes:\n")
		fmt.Fprintf(os.Stderr, "  0  Success (payment accepted or probe completed)\n")
		fmt.Fprintf(os.Stderr, "  1  Error (network, config, or unexpected failure)\n")
		fmt.Fprintf(os.Stderr, "  2  Payment rejected by facilitator\n")
		fmt.Fprintf(os.Stderr, "  3  Route is free (no payment needed)\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  EVM_PRIVATE_KEY    Private key for signing payments (required)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if showVer {
		if jsonOutput {
			out, _ := json.Marshal(map[string]string{"version": version})
			fmt.Println(string(out))
		} else {
			fmt.Printf("x402-cli %s\n", version)
		}
		os.Exit(0)
	}

	endpoint := flag.Arg(0)
	if endpoint == "" {
		if jsonOutput {
			exitJSON(&jsonResult{Version: version, Status: "error", Error: "URL argument is required"}, ExitError)
		}
		fmt.Fprintln(os.Stderr, "Error: URL argument is required")
		fmt.Fprintln(os.Stderr, "Usage: x402-cli [flags] <url>")
		os.Exit(ExitError)
	}

	// If -d is set and method was not explicitly changed, default to POST.
	if data != "" && method == "GET" {
		method = "POST"
	}

	// quiet implies no human-readable output (JSON still prints).
	log := func(format string, a ...any) {
		if !quiet && !jsonOutput {
			fmt.Printf(format, a...)
		}
	}
	logln := func(a ...any) {
		if !quiet && !jsonOutput {
			fmt.Println(a...)
		}
	}

	privateKey := os.Getenv("EVM_PRIVATE_KEY")

	transport := &http.Transport{}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Build JSON result for --json mode.
	result := &jsonResult{
		Version:  version,
		Endpoint: endpoint,
		Method:   method,
	}

	log("x402-cli %s\n", version)
	log("Endpoint: %s\n", endpoint)
	log("Method:   %s\n\n", method)

	// --- Step 1: Request without payment → expect 402 ---
	logln("--- Step 1: Request without payment ---")

	plainClient := &http.Client{Transport: transport, Timeout: timeout}
	req, err := newRequest(method, endpoint, data, headers)
	if err != nil {
		if jsonOutput {
			result.Status = "error"
			result.Error = err.Error()
			exitJSON(result, ExitError)
		}
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(ExitError)
	}

	if verbose && !quiet && !jsonOutput {
		dumpRequest(req)
	}

	resp, err := plainClient.Do(req)
	if err != nil {
		if jsonOutput {
			result.Status = "error"
			result.Error = err.Error()
			exitJSON(result, ExitError)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(ExitError)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if verbose && !quiet && !jsonOutput {
		dumpResponse(resp, body)
	}

	// Build probe result.
	probe := &probeResult{
		StatusCode:      resp.StatusCode,
		PaymentRequired: resp.StatusCode == http.StatusPaymentRequired,
	}
	if payReqHeader := resp.Header.Get("PAYMENT-REQUIRED"); payReqHeader != "" {
		if decoded, err := base64.StdEncoding.DecodeString(payReqHeader); err == nil {
			raw := json.RawMessage(decoded)
			probe.PaymentRequirements = &raw
		}
		if !quiet && !jsonOutput {
			printBase64Header("PAYMENT-REQUIRED", payReqHeader)
		}
	}
	if !jsonOutput {
		log("Status: %d\n", resp.StatusCode)
		if !verbose {
			log("Body: %s\n\n", truncate(string(body), 300))
		}
	}
	probe.Body = string(body)
	result.Probe = probe

	if resp.StatusCode != http.StatusPaymentRequired {
		logln("Endpoint did not return 402 Payment Required.")
		if resp.StatusCode == http.StatusOK {
			logln("The endpoint is accessible without payment (free route).")
			saveOutput(outputFile, body)
			result.Status = "free"
			if jsonOutput {
				exitJSON(result, ExitFreeRoute)
			}
			os.Exit(ExitFreeRoute)
		}
		result.Status = "no_402"
		if jsonOutput {
			exitJSON(result, ExitSuccess)
		}
		os.Exit(ExitSuccess)
	}

	if skipVerify {
		logln("--skip-verify: stopping after Step 1.")
		result.Status = "payment_required"
		if jsonOutput {
			exitJSON(result, ExitSuccess)
		}
		os.Exit(ExitSuccess)
	}

	// --- Dry-run: show cost and confirm ---
	if dryRun && !autoYes {
		if jsonOutput {
			// In JSON mode, dry-run without -y just returns the requirements.
			result.Status = "payment_required"
			exitJSON(result, ExitSuccess)
		}
		printPaymentSummary(body)
		fmt.Print("\nProceed with payment? [y/N] ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(scanner.Text())), "y") {
			fmt.Println("Aborted.")
			os.Exit(ExitSuccess)
		}
		fmt.Println()
	}

	// --- Step 2: Request with x402 payment ---
	if privateKey == "" {
		errMsg := "EVM_PRIVATE_KEY is required for Step 2 (payment)"
		if jsonOutput {
			result.Status = "error"
			result.Error = errMsg
			exitJSON(result, ExitError)
		}
		fmt.Fprintln(os.Stderr, "\nError: "+errMsg+".")
		fmt.Fprintln(os.Stderr, "Set it with: export EVM_PRIVATE_KEY=0x...")
		os.Exit(ExitError)
	}

	logln("--- Step 2: Request with x402 payment ---")

	evmSigner, err := evmsigners.NewClientSignerFromPrivateKey(privateKey)
	if err != nil {
		if jsonOutput {
			result.Status = "error"
			result.Error = "failed to create signer: " + err.Error()
			exitJSON(result, ExitError)
		}
		fmt.Fprintf(os.Stderr, "Failed to create signer: %v\n", err)
		os.Exit(ExitError)
	}
	log("Signer: %s\n", evmSigner.Address())

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
		if jsonOutput {
			result.Status = "error"
			result.Error = "payment request failed: " + err.Error()
			exitJSON(result, ExitError)
		}
		fmt.Fprintf(os.Stderr, "Payment request failed: %v\n", err)
		os.Exit(ExitError)
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)

	if verbose && !quiet && !jsonOutput {
		dumpResponse(resp2, body2)
	}

	// Build payment result.
	pay := &payResult{
		StatusCode: resp2.StatusCode,
		Accepted:   resp2.StatusCode == http.StatusOK,
		Signer:     evmSigner.Address(),
		Body:       string(body2),
	}
	if payRespHeader := resp2.Header.Get("PAYMENT-RESPONSE"); payRespHeader != "" {
		if decoded, err := base64.StdEncoding.DecodeString(payRespHeader); err == nil {
			raw := json.RawMessage(decoded)
			pay.PaymentResponse = &raw
		}
		if !quiet && !jsonOutput {
			printBase64Header("PAYMENT-RESPONSE", payRespHeader)
		}
	}
	result.Payment = pay

	if !jsonOutput {
		log("Status: %d\n", resp2.StatusCode)
		if !verbose {
			log("Body: %s\n\n", truncate(string(body2), 500))
		}
	}

	// Save response body to file if -o is set.
	saveOutput(outputFile, body2)

	switch resp2.StatusCode {
	case http.StatusOK:
		logln("Payment accepted!")
		result.Status = "accepted"
		if jsonOutput {
			exitJSON(result, ExitSuccess)
		}
		os.Exit(ExitSuccess)
	case http.StatusPaymentRequired:
		logln("Payment was rejected. Check wallet balance and facilitator logs.")
		result.Status = "rejected"
		if jsonOutput {
			exitJSON(result, ExitPaymentRejected)
		}
		os.Exit(ExitPaymentRejected)
	default:
		log("Unexpected status %d.\n", resp2.StatusCode)
		result.Status = "error"
		result.Error = fmt.Sprintf("unexpected status %d", resp2.StatusCode)
		if jsonOutput {
			exitJSON(result, ExitError)
		}
		os.Exit(ExitError)
	}
}

// saveOutput writes body to a file if outputFile is set.
func saveOutput(outputFile string, body []byte) {
	if outputFile == "" || len(body) == 0 {
		return
	}
	if err := os.WriteFile(outputFile, body, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write to %s: %v\n", outputFile, err)
	}
}

// exitJSON marshals the result to stdout and exits.
func exitJSON(result *jsonResult, code int) {
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
	os.Exit(code)
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
func printPaymentSummary(body []byte) {
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

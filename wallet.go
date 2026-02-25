package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	evmsigners "github.com/coinbase/x402/go/signers/evm"
)

// networkInfo holds RPC and USDC contract info for a network.
type networkInfo struct {
	ChainID      string
	RPCURL       string
	USDCContract string
	Decimals     int
	Name         string
}

var networks = map[string]networkInfo{
	"base": {
		ChainID:      "eip155:8453",
		RPCURL:       "https://mainnet.base.org",
		USDCContract: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		Decimals:     6,
		Name:         "Base",
	},
	"base-sepolia": {
		ChainID:      "eip155:84532",
		RPCURL:       "https://sepolia.base.org",
		USDCContract: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Decimals:     6,
		Name:         "Base Sepolia",
	},
	"avalanche": {
		ChainID:      "eip155:43114",
		RPCURL:       "https://api.avax.network/ext/bc/C/rpc",
		USDCContract: "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E",
		Decimals:     6,
		Name:         "Avalanche",
	},
	"avalanche-fuji": {
		ChainID:      "eip155:43113",
		RPCURL:       "https://api.avax-test.network/ext/bc/C/rpc",
		USDCContract: "0x5425890298aed601595a70AB815c96711a31Bc65",
		Decimals:     6,
		Name:         "Avalanche Fuji",
	},
}

// walletResult is the JSON output for `x402-cli wallet`.
type walletResult struct {
	Address  string           `json:"address"`
	Balances []balanceEntry   `json:"balances"`
	Error    string           `json:"error,omitempty"`
}

type balanceEntry struct {
	Network  string `json:"network"`
	ChainID  string `json:"chainId"`
	Asset    string `json:"asset"`
	Balance  string `json:"balance"`
	Decimals int    `json:"decimals"`
	Raw      string `json:"raw"`
}

// runWallet shows wallet address and USDC balances.
func runWallet(address string, network string, jsonOutput bool) {
	result := &walletResult{Address: address}

	// If specific network requested, only query that one.
	netsToQuery := networks
	if network != "" {
		if info, ok := networks[network]; ok {
			netsToQuery = map[string]networkInfo{network: info}
		} else {
			if jsonOutput {
				result.Error = fmt.Sprintf("unknown network: %s", network)
				out, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(out))
				return
			}
			fmt.Fprintf(os.Stderr, "Unknown network: %s\n", network)
			fmt.Fprintf(os.Stderr, "Available: %s\n", availableNetworks())
			return
		}
	}

	if !jsonOutput {
		fmt.Printf("Wallet:  %s\n\n", address)
	}

	for name, info := range netsToQuery {
		humanBalance, raw, err := queryUSDCBalance(info.RPCURL, info.USDCContract, address)
		if err != nil {
			entry := balanceEntry{
				Network: name,
				ChainID: info.ChainID,
				Asset:   "USDC",
				Balance: "error",
				Raw:     err.Error(),
			}
			result.Balances = append(result.Balances, entry)
			if !jsonOutput {
				fmt.Printf("  %-18s  error: %v\n", info.Name+" (USDC):", err)
			}
			continue
		}
		entry := balanceEntry{
			Network:  name,
			ChainID:  info.ChainID,
			Asset:    "USDC",
			Balance:  humanBalance,
			Decimals: info.Decimals,
			Raw:      raw,
		}
		result.Balances = append(result.Balances, entry)

		if !jsonOutput {
			fmt.Printf("  %-18s  %s USDC\n", info.Name+":", humanBalance)
		}
	}

	if jsonOutput {
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(out))
	}
}

// queryUSDCBalance calls balanceOf on the USDC contract via JSON-RPC.
func queryUSDCBalance(rpcURL, contractAddr, walletAddr string) (string, string, error) {
	// balanceOf(address) selector = 0x70a08231
	// address padded to 32 bytes
	addr := strings.TrimPrefix(strings.ToLower(walletAddr), "0x")
	callData := "0x70a08231" + fmt.Sprintf("%064s", addr)

	rpcReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_call",
		"params": []any{
			map[string]string{
				"to":   contractAddr,
				"data": callData,
			},
			"latest",
		},
	}

	body, _ := json.Marshal(rpcReq)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(rpcURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("rpc call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var rpcResp struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return "", "", fmt.Errorf("invalid rpc response")
	}
	if rpcResp.Error != nil {
		return "", "", fmt.Errorf("rpc error: %s", rpcResp.Error.Message)
	}

	// Parse hex result to big.Int.
	hexStr := strings.TrimPrefix(rpcResp.Result, "0x")
	if hexStr == "" || hexStr == "0" {
		return "0", "0", nil
	}

	balanceBytes, err := hex.DecodeString(padHexLeft(hexStr))
	if err != nil {
		return "", "", fmt.Errorf("invalid hex: %s", hexStr)
	}

	raw := new(big.Int).SetBytes(balanceBytes).String()
	return atomicToHuman(raw, 6), raw, nil
}

// atomicToHuman converts atomic units (e.g., "1000") to human readable (e.g., "0.001").
func atomicToHuman(raw string, decimals int) string {
	bal := new(big.Int)
	bal.SetString(raw, 10)

	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)

	whole := new(big.Int).Div(bal, divisor)
	frac := new(big.Int).Mod(bal, divisor)

	if frac.Sign() == 0 {
		return whole.String() + ".000000"
	}
	fracStr := fmt.Sprintf("%0*s", decimals, frac.String())
	// Trim trailing zeros but keep at least 2 decimal places.
	trimmed := strings.TrimRight(fracStr, "0")
	if len(trimmed) < 2 {
		trimmed = fracStr[:2]
	}
	return whole.String() + "." + trimmed
}

// padHexLeft pads a hex string to even length.
func padHexLeft(s string) string {
	if len(s)%2 != 0 {
		return "0" + s
	}
	return s
}

func availableNetworks() string {
	names := make([]string, 0, len(networks))
	for name := range networks {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// runWalletCmd parses wallet subcommand flags and runs.
func runWalletCmd(args []string) {
	fs := flag.NewFlagSet("wallet", flag.ExitOnError)
	var network string
	var jsonOut bool
	fs.StringVar(&network, "network", "", "Query specific network (default: all)")
	fs.BoolVar(&jsonOut, "json", false, "Output JSON")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: x402-cli wallet [--network <name>] [--json]\n\n")
		fmt.Fprintf(os.Stderr, "Shows wallet address and USDC balance from EVM_PRIVATE_KEY.\n\n")
		fmt.Fprintf(os.Stderr, "Networks: %s\n\n", availableNetworks())
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	privateKey := os.Getenv("EVM_PRIVATE_KEY")
	if privateKey == "" {
		fmt.Fprintln(os.Stderr, "Error: EVM_PRIVATE_KEY is required.")
		fmt.Fprintln(os.Stderr, "Set it with: export EVM_PRIVATE_KEY=0x...")
		os.Exit(1)
	}

	signer, err := evmsigners.NewClientSignerFromPrivateKey(privateKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create signer: %v\n", err)
		os.Exit(1)
	}

	runWallet(signer.Address(), network, jsonOut)
}

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vinayprograms/agent/internal/packaging"
)

// generateKeys generates a new signing key pair.
func generateKeys(args []string) {
	outputPrefix := parseKeygenArgs(args)

	privPath := outputPrefix + ".pem"
	pubPath := outputPrefix + ".pub"

	if err := checkKeyPaths(privPath, pubPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	pubKey, privKey, err := packaging.GenerateKeyPair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating key pair: %v\n", err)
		os.Exit(1)
	}

	if err := saveKeyPair(privPath, pubPath, privKey, pubKey); err != nil {
		fmt.Fprintf(os.Stderr, "error saving keys: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Generated key pair\n")
	fmt.Printf("  Private key: %s (keep secret!)\n", privPath)
	fmt.Printf("  Public key:  %s (share for verification)\n", pubPath)
}

func parseKeygenArgs(args []string) string {
	outputPrefix := "agent-key"

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case (arg == "--output" || arg == "-o") && i+1 < len(args):
			i++
			outputPrefix = args[i]
		case strings.HasPrefix(arg, "--output="):
			outputPrefix = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			outputPrefix = strings.TrimPrefix(arg, "-o=")
		}
	}

	return outputPrefix
}

func checkKeyPaths(privPath, pubPath string) error {
	if _, err := os.Stat(privPath); err == nil {
		return fmt.Errorf("%s already exists", privPath)
	}
	if _, err := os.Stat(pubPath); err == nil {
		return fmt.Errorf("%s already exists", pubPath)
	}
	return nil
}

func saveKeyPair(privPath, pubPath string, privKey, pubKey []byte) error {
	if err := packaging.SavePrivateKey(privPath, privKey); err != nil {
		return fmt.Errorf("saving private key: %w", err)
	}
	if err := packaging.SavePublicKey(pubPath, pubKey); err != nil {
		return fmt.Errorf("saving public key: %w", err)
	}
	return nil
}

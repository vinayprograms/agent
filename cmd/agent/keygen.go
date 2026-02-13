package main

import (
	"fmt"
	"os"

	"github.com/vinayprograms/agent/internal/packaging"
)

// runKeygen generates a new signing key pair.
func runKeygen(outputPrefix string) error {
	privPath := outputPrefix + ".pem"
	pubPath := outputPrefix + ".pub"

	if err := checkKeyPaths(privPath, pubPath); err != nil {
		return err
	}

	pubKey, privKey, err := packaging.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generating key pair: %w", err)
	}

	if err := saveKeyPair(privPath, pubPath, privKey, pubKey); err != nil {
		return fmt.Errorf("saving keys: %w", err)
	}

	fmt.Printf("✓ Generated key pair\n")
	fmt.Printf("  Private key: %s (keep secret!)\n", privPath)
	fmt.Printf("  Public key:  %s (share for verification)\n", pubPath)
	return nil
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

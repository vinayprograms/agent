package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/packaging"
)

// newKeygenCmd generates a signing key pair.
func newKeygenCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate signing key pair",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runKeygen(cmd.OutOrStdout(), output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "agent-key", "Output path prefix (creates .pem and .pub)")
	return cmd
}

// runKeygen generates a new signing key pair.
func runKeygen(w io.Writer, outputPrefix string) error {
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

	fmt.Fprintf(w, "✓ Generated key pair\n")
	fmt.Fprintf(w, "  Private key: %s (keep secret!)\n", privPath)
	fmt.Fprintf(w, "  Public key:  %s (share for verification)\n", pubPath)
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

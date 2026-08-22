package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vinayprograms/agent/internal/packaging"
)

// newVerifyCmd verifies a package's signature and content hash.
func newVerifyCmd() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "verify <package>",
		Short: "Verify package signature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerify(args[0], key)
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "Public key path for verification")
	return cmd
}

// runVerify verifies a package signature.
func runVerify(pkgPath, keyPath string) error {
	pkg, err := packaging.Load(pkgPath)
	if err != nil {
		return fmt.Errorf("loading package: %w", err)
	}

	var pubKey []byte
	if keyPath != "" {
		pubKey, err = packaging.LoadPublicKey(keyPath)
		if err != nil {
			return fmt.Errorf("loading public key: %w", err)
		}
	}

	if err := packaging.Verify(pkg, pubKey); err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}

	printVerifyResult(pkg)
	return nil
}

func printVerifyResult(pkg *packaging.Package) {
	fmt.Printf("✓ Package verified: %s@%s\n", pkg.Manifest.Name, pkg.Manifest.Version)
	if pkg.Signature != nil {
		fmt.Println("  Signature: valid")
	} else {
		fmt.Println("  Signature: unsigned")
	}
	fmt.Println("  Content hash: valid")
}

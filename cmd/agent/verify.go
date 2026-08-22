package main

import (
	"fmt"
	"io"

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
			return runVerify(cmd.OutOrStdout(), args[0], key)
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "Public key path for verification")
	return cmd
}

// runVerify verifies a package signature.
func runVerify(w io.Writer, pkgPath, keyPath string) error {
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

	printVerifyResult(w, pkg)
	return nil
}

func printVerifyResult(w io.Writer, pkg *packaging.Package) {
	fmt.Fprintf(w, "✓ Package verified: %s@%s\n", pkg.Manifest.Name, pkg.Manifest.Version)
	if pkg.Signature != nil {
		fmt.Fprintln(w, "  Signature: valid")
	} else {
		fmt.Fprintln(w, "  Signature: unsigned")
	}
	fmt.Fprintln(w, "  Content hash: valid")
}

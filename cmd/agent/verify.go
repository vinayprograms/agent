package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vinayprograms/agent/internal/packaging"
)

// verifyPackage verifies a package signature.
func verifyPackage(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: package path required")
		os.Exit(1)
	}

	pkgPath := args[0]
	pubKeyPath := parseVerifyArgs(args[1:])

	pkg, err := packaging.Load(pkgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading package: %v\n", err)
		os.Exit(1)
	}

	var pubKey []byte
	if pubKeyPath != "" {
		pubKey, err = packaging.LoadPublicKey(pubKeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading public key: %v\n", err)
			os.Exit(1)
		}
	}

	if err := packaging.Verify(pkg, pubKey); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Verification failed: %v\n", err)
		os.Exit(1)
	}

	printVerifyResult(pkg)
}

func parseVerifyArgs(args []string) string {
	var pubKeyPath string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--key" && i+1 < len(args):
			i++
			pubKeyPath = args[i]
		case strings.HasPrefix(arg, "--key="):
			pubKeyPath = strings.TrimPrefix(arg, "--key=")
		}
	}
	return pubKeyPath
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

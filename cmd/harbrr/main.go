// Command harbrr is a Cardigann-compatible Torznab/Newznab search provider for
// the autobrr family. See docs/architecture.md for the design.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "harbrr:", err)
		os.Exit(1)
	}
}

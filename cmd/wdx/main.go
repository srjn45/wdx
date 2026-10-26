package main

import (
	"fmt"
	"os"

	"github.com/srjn45/warden/internal/cli"
	"github.com/srjn45/warden/internal/identity"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", identity.Name, err)
		os.Exit(1)
	}
}

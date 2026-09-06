package main

import (
	"codex/platform-demo/internal/jobcli"
	"fmt"
	"os"
)

func main() {
	if err := jobcli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

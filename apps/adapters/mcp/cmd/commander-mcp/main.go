package main

import (
	"os"

	"commander/adapters/mcp/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

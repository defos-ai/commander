package main

import (
	"os"

	"github.com/defos-ai/commander/apps/adapters/mcp/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

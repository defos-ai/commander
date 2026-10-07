package main

import (
	"os"

	"github.com/defos-ai/commander/apps/agents/web-quality/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

package main

import (
	"os"

	"github.com/defos-ai/commander/apps/agents/spawner/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

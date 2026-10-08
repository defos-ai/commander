package main

import (
	"os"

	"github.com/defos-ai/commander/apps/adapters/api/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr)) }

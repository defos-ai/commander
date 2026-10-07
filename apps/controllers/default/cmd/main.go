package main

import (
	"github.com/defos-ai/commander/apps/controllers/default/cli"
	"os"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

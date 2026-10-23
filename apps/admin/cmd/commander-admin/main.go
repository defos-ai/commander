package main

import (
	"os"

	"github.com/defos-ai/commander/apps/admin/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

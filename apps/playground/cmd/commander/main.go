package main

import (
	"github.com/defos-ai/commander/apps/playground/cli"
	"os"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

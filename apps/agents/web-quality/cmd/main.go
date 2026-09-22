package main

import (
	"os"

	"commander/apps/agents/web-quality/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

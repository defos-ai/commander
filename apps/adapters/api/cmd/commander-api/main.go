package main

import (
	"os"

	"commander/adapters/api/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

package main

import (
	"commander/apps/controllers/default/cli"
	"os"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

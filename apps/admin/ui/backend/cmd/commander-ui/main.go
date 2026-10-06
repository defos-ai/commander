package main

import (
	"os"

	"commander/admin/ui/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

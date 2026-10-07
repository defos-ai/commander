package main

import (
	"os"

	"github.com/defos-ai/commander/apps/admin/ui/backend/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stderr)) }

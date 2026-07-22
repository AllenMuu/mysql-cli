package main

import (
	"os"

	"github.com/AllenMuu/mysql-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}

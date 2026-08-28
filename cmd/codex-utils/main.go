package main

import (
	"os"

	"github.com/chensunlai/codex-utils/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}

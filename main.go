package main

import (
	"fmt"
	"os"

	"github.com/Kaua-KGzin/arch-deploy/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "erro: %v\n", err)
		os.Exit(1)
	}
}

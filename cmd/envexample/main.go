package main

import (
	"fmt"
	"os"

	"github.com/OverlayFox/VRC-Haven/src"
)

func main() {
	if err := src.WriteEnvExample(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

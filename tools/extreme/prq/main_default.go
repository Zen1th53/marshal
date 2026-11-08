//go:build governed

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "prq requires the default Marshal build")
	os.Exit(1)
}

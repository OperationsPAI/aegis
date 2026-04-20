package main

import (
	"aegis/cmd/aegisctl/cmd"
	"os"
)

func main() {
	os.Exit(cmd.Execute())
}

package main

import (
	blobapp "aegis/boot/blob"
	"aegis/cmd/cmdutil"
)

func main() {
	cmdutil.RunServe("aegis-blob", "Aegis blob storage microservice", "8085", blobapp.Options)
}

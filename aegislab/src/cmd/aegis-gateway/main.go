package main

import (
	gateway "aegis/boot/gateway"
	"aegis/cmd/cmdutil"
)

func main() {
	cmdutil.RunServe("aegis-gateway", "Aegis L7 API gateway", "8086", gateway.Options)
}

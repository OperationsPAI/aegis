package main

import (
	chaosapp "aegis/boot/chaos"
	"aegis/cmd/cmdutil"
)

func main() {
	cmdutil.RunServe("aegis-chaos", "Aegis pluggable fault-injection microservice", "8086", chaosapp.Options)
}

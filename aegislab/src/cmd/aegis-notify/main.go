package main

import (
	notify "aegis/boot/notify"
	"aegis/cmd/cmdutil"
)

func main() {
	cmdutil.RunServe("aegis-notify", "Aegis notification microservice", "8084", notify.Options)
}

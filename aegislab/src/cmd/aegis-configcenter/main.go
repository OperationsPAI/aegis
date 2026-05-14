package main

import (
	configcenter "aegis/boot/configcenter"
	"aegis/cmd/cmdutil"
)

func main() {
	cmdutil.RunServe("aegis-configcenter", "Aegis configuration-center microservice", "8087", configcenter.Options)
}

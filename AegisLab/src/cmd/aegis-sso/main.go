package main

import (
	sso "aegis/boot/sso"
	"aegis/cmd/cmdutil"
)

func main() {
	cmdutil.RunServe("aegis-sso", "Aegis SSO identity service", "8083", sso.Options)
}

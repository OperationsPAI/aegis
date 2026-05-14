package main

import (
	"flag"

	runtimeapp "aegis/boot/runtime"

	"go.uber.org/fx"
)

func main() {
	conf := flag.String("conf", "/etc/aegis/config.prod.toml", "path to configuration file")
	flag.Parse()

	fx.New(runtimeapp.Options(*conf)).Run()
}

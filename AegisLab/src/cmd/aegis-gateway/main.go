package main

import (
	"os"

	gateway "aegis/app/gateway"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func main() {
	var port, conf string

	rootCmd := &cobra.Command{Use: "aegis-gateway", Short: "Aegis L7 API gateway"}
	rootCmd.PersistentFlags().StringVarP(&port, "port", "p", "8086", "Port to run the gateway on")
	rootCmd.PersistentFlags().StringVarP(&conf, "conf", "c", "/etc/aegis/config.prod.toml", "Path to configuration directory")

	if err := viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}
	if err := viper.BindPFlag("conf", rootCmd.PersistentFlags().Lookup("conf")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}

	rootCmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the gateway server",
		Run: func(cmd *cobra.Command, args []string) {
			fx.New(gateway.Options(viper.GetString("conf"), viper.GetString("port"))).Run()
		},
	})

	if err := rootCmd.Execute(); err != nil {
		logrus.Println(err.Error())
		os.Exit(1)
	}
}

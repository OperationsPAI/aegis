package main

import (
	"os"

	notify "aegis/app/notify"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func main() {
	var port, conf string

	rootCmd := &cobra.Command{Use: "aegis-notify", Short: "Aegis notification microservice"}
	rootCmd.PersistentFlags().StringVarP(&port, "port", "p", "8084", "Port to run the server on")
	rootCmd.PersistentFlags().StringVarP(&conf, "conf", "c", "/etc/aegis/config.prod.toml", "Path to configuration directory")

	if err := viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}
	if err := viper.BindPFlag("conf", rootCmd.PersistentFlags().Lookup("conf")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}

	rootCmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the notification server",
		Run: func(cmd *cobra.Command, args []string) {
			fx.New(notify.Options(viper.GetString("conf"), viper.GetString("port"))).Run()
		},
	})

	if err := rootCmd.Execute(); err != nil {
		logrus.Println(err.Error())
		os.Exit(1)
	}
}

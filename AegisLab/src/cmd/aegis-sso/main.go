package main

import (
	"os"

	sso "aegis/boot/sso"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func main() {
	var port string
	var conf string

	rootCmd := &cobra.Command{
		Use:   "sso",
		Short: "Aegis SSO identity service",
	}

	rootCmd.PersistentFlags().StringVarP(&port, "port", "p", "8083", "Port to run the server on")
	rootCmd.PersistentFlags().StringVarP(&conf, "conf", "c", "/etc/aegis/config.prod.toml", "Path to configuration file")

	if err := viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}
	if err := viper.BindPFlag("conf", rootCmd.PersistentFlags().Lookup("conf")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}

	ssoCmd := &cobra.Command{
		Use:   "sso",
		Short: "Run the SSO server",
		Run: func(cmd *cobra.Command, args []string) {
			fx.New(sso.Options(viper.GetString("conf"), viper.GetString("port"))).Run()
		},
	}

	rootCmd.AddCommand(ssoCmd)
	if err := rootCmd.Execute(); err != nil {
		logrus.Println(err.Error())
		os.Exit(1)
	}
}

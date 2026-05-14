//	@title			RCABench API
//	@version		1.0.0
//	@description	RCABench - A comprehensive root cause analysis benchmarking platform for microservices

//	@contact.name	RCABench Team
//	@contact.email	team@rcabench.com

//	@license.name	Apache 2.0
//	@license.url	http://www.apache.org/licenses/LICENSE-2.0.html

//	@host	http://localhost:8082

//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Type "Bearer" followed by a space and JWT token.

package main

import (
	"os"

	"aegis/boot"
	apiboot "aegis/boot/api"
	runtimeapp "aegis/boot/runtime"
	sso "aegis/boot/sso"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func newModeCommand(use, short string, run func()) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Run: func(cmd *cobra.Command, args []string) {
			run()
		},
	}
}

func main() {
	var port string
	var conf string
	var rootCmd = &cobra.Command{
		Use:   "rcabench",
		Short: "RCA Bench is a benchmarking tool",
		Run: func(cmd *cobra.Command, args []string) {
			logrus.Println("Please specify a mode: producer, consumer, both, aegis-api, runtime-worker-service, or sso")
		},
	}

	rootCmd.PersistentFlags().StringVarP(&port, "port", "p", "8080", "Port to run the server on")
	rootCmd.PersistentFlags().StringVarP(&conf, "conf", "c", "/etc/aegis/config.prod.toml", "Path to configuration file")

	if err := viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}
	if err := viper.BindPFlag("conf", rootCmd.PersistentFlags().Lookup("conf")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}

	producerCmd := newModeCommand("producer", "Run as a producer", func() {
		fx.New(app.ProducerOptions(viper.GetString("conf"), viper.GetString("port"))).Run()
	})
	consumerCmd := newModeCommand("consumer", "Run as a consumer", func() {
		fx.New(app.ConsumerOptions(viper.GetString("conf"))).Run()
	})
	bothCmd := newModeCommand("both", "Run as both producer and consumer", func() {
		fx.New(app.BothOptions(viper.GetString("conf"), viper.GetString("port"))).Run()
	})
	aegisAPICmd := newModeCommand("aegis-api", "Run as the aegis business API process", func() {
		fx.New(apiboot.Options(viper.GetString("conf"), viper.GetString("port"))).Run()
	})
	runtimeWorkerServiceCmd := newModeCommand("runtime-worker-service", "Run as the runtime worker service", func() {
		fx.New(runtimeapp.Options(viper.GetString("conf"))).Run()
	})
	ssoCmd := newModeCommand("sso", "Run as the SSO identity service", func() {
		port := viper.GetString("port")
		if port == "" || port == "8080" {
			port = "8083"
		}
		fx.New(sso.Options(viper.GetString("conf"), port)).Run()
	})

	rootCmd.AddCommand(
		producerCmd,
		consumerCmd,
		bothCmd,
		aegisAPICmd,
		runtimeWorkerServiceCmd,
		ssoCmd,
	)
	if err := rootCmd.Execute(); err != nil {
		logrus.Println(err.Error())
		os.Exit(1)
	}
}

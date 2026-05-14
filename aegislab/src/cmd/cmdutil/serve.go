package cmdutil

import (
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

// RunServe wires up a standard cobra root + `serve` subcommand for the
// boot/<svc> microservices. Each binary differs only in name, short help,
// default port, and the fx.Option provider.
func RunServe(name, short, defaultPort string, optsFn func(conf, port string) fx.Option) {
	var port, conf string

	rootCmd := &cobra.Command{Use: name, Short: short}
	rootCmd.PersistentFlags().StringVarP(&port, "port", "p", defaultPort, "Port to run the server on")
	rootCmd.PersistentFlags().StringVarP(&conf, "conf", "c", "/etc/aegis/config.prod.toml", "Path to configuration file")

	if err := viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}
	if err := viper.BindPFlag("conf", rootCmd.PersistentFlags().Lookup("conf")); err != nil {
		logrus.Fatalf("failed to bind flag: %v", err)
	}

	rootCmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the " + name + " server",
		Run: func(cmd *cobra.Command, args []string) {
			fx.New(optsFn(viper.GetString("conf"), viper.GetString("port"))).Run()
		},
	})

	if err := rootCmd.Execute(); err != nil {
		logrus.Println(err.Error())
		os.Exit(1)
	}
}

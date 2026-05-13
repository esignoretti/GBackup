package cmd

import (
	"fmt"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/storage"
	"github.com/esignoretti/gbackup/web"
	"github.com/spf13/cobra"
)

var servePort int

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		store, err := storage.NewClient(&cfg.Storage)
		if err != nil {
			return err
		}
		srv := &web.Server{
			Config: cfg,
			Store:  store,
			Addr:   fmt.Sprintf("localhost:%d", servePort),
		}
		return srv.Start()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntVar(&servePort, "port", 8080, "Web dashboard port")
}

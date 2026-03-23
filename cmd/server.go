package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/score-spec/score-orchestrator/api"
)

var serverPort int

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the HTTP REST server",
	RunE: func(cmd *cobra.Command, args []string) error {
		runner, err := buildRunner(cfg)
		if err != nil {
			return err
		}
		// Reuse the backend already wired into the runner — avoids creating a second client.
		srv := api.NewServer(runner, runner.Backend, serverPort)
		fmt.Printf("score-orchestrator server listening on :%d\n", serverPort)
		return srv.Start()
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().IntVar(&serverPort, "port", 8080, "HTTP server port")
}

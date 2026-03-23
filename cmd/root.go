package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/deepakvbansode/platform-orchestrator/internal/config"
)

var (
	cfgFile string
	cfg     *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "score-orchestrator",
	Short: "Thin orchestrator for score-k8s: state backends, provisioner sync, and deployment",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config %q: %w", cfgFile, err)
		}
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "orchestrator.yaml", "path to orchestrator.yaml")
}

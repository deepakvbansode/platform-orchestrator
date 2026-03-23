package cmd

import (
	"fmt"
	"os"

	"github.com/deepakvbansode/platform-orchestrator/internal/config"
	"github.com/deepakvbansode/platform-orchestrator/internal/logger"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	debug   bool
	cfg     *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "score-orchestrator",
	Short: "Thin orchestrator for score-k8s: state backends, provisioner sync, and deployment",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		logger.Init(debug)
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
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", true, "enable debug logging") //Todo: change it back to falst, it should come from env
}

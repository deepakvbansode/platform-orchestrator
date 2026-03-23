package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/deepakvbansode/platform-orchestrator/internal/pipeline"
)

var (
	deployScoreFile string
	deployOrg       string
	deployEnv       string
	deployWorkload  string
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Run the deploy pipeline for a workload",
	RunE: func(cmd *cobra.Command, args []string) error {
		scoreYAML, err := os.ReadFile(deployScoreFile)
		if err != nil {
			return fmt.Errorf("read score file: %w", err)
		}

		runner, err := buildRunner(cfg)
		if err != nil {
			return err
		}

		result, oe := runner.Run(context.Background(), pipeline.DeployRequest{
			Org:       deployOrg,
			Env:       deployEnv,
			Workload:  deployWorkload,
			ScoreYAML: scoreYAML,
		})
		if oe != nil && result == nil {
			fmt.Fprintf(os.Stderr, "error [%s]: %s\n", oe.Code, oe.Message)
			if oe.Detail != "" {
				fmt.Fprintf(os.Stderr, "detail: %s\n", oe.Detail)
			}
			os.Exit(1)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
		if oe != nil {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deployCmd)
	deployCmd.Flags().StringVar(&deployScoreFile, "score", "score.yaml", "path to score.yaml")
	deployCmd.Flags().StringVar(&deployOrg, "org", "", "organisation identifier")
	deployCmd.Flags().StringVar(&deployEnv, "env", "", "environment (e.g. prod, staging)")
	deployCmd.Flags().StringVar(&deployWorkload, "workload", "", "workload name")
	deployCmd.MarkFlagRequired("org")
	deployCmd.MarkFlagRequired("env")
	deployCmd.MarkFlagRequired("workload")
}

package cmd

import (
	"fmt"
	"strconv"

	"aegis/cli/apiclient"
	"aegis/cli/output"

	"github.com/spf13/cobra"
)

var evalCmd = &cobra.Command{
	Use:     "eval",
	Aliases: []string{"evaluation"},
	Short:   "Manage evaluations",
}

// --- eval list ---

var evalListPage int
var evalListSize int

var evalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List evaluations",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, ctx := newAPIClient()
		resp, _, err := cli.EvaluationsAPI.ListEvaluations(ctx).
			Page(int32(evalListPage)).
			Size(int32(evalListSize)).
			Execute()
		if err != nil {
			return err
		}

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(resp.Data)
			return nil
		}

		var items []apiclient.EvaluationEvaluationResp
		if resp.Data != nil {
			items = resp.Data.GetItems()
		}
		rows := make([][]string, 0, len(items))
		for _, e := range items {
			rows = append(rows, []string{
				strconv.FormatInt(int64(e.GetId()), 10),
				e.GetAlgorithmName(),
				e.GetEvalType(),
				"",
				e.GetCreatedAt(),
			})
		}
		output.PrintTable([]string{"ID", "Name", "Type", "Status", "Created"}, rows)
		return nil
	},
}

// --- eval get ---

var evalGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get evaluation details by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		idInt, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("evaluation id must be numeric: %w", err)
		}
		cli, ctx := newAPIClient()
		resp, _, err := cli.EvaluationsAPI.GetEvaluationById(ctx, int32(idInt)).Execute()
		if err != nil {
			return err
		}
		d := resp.Data

		if output.OutputFormat(flagOutput) == output.FormatJSON {
			output.PrintJSON(d)
			return nil
		}
		if d == nil {
			return fmt.Errorf("empty evaluation response")
		}

		fmt.Printf("ID:        %d\n", d.GetId())
		fmt.Printf("Name:      %s\n", d.GetAlgorithmName())
		fmt.Printf("Type:      %s\n", d.GetEvalType())
		fmt.Printf("Status:    %s\n", "")
		fmt.Printf("Project:   %d\n", d.GetProjectId())
		fmt.Printf("Created:   %s\n", d.GetCreatedAt())
		fmt.Printf("Updated:   %s\n", d.GetUpdatedAt())
		if rj := d.GetResultJson(); rj != "" {
			fmt.Printf("Result:\n%s\n", rj)
		}
		return nil
	},
}

func init() {
	evalListCmd.Flags().IntVar(&evalListPage, "page", 1, "Page number")
	evalListCmd.Flags().IntVar(&evalListSize, "size", 20, "Page size")

	evalCmd.AddCommand(evalListCmd)
	evalCmd.AddCommand(evalGetCmd)
}

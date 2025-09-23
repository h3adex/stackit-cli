package routingtable

import (
	"github.com/spf13/cobra"
	rtDescribe "github.com/stackitcloud/stackit-cli/internal/cmd/beta/routingtable/describe"
	rtList "github.com/stackitcloud/stackit-cli/internal/cmd/beta/routingtable/list"
	route "github.com/stackitcloud/stackit-cli/internal/cmd/beta/routingtable/route"
	"github.com/stackitcloud/stackit-cli/internal/cmd/params"
	"github.com/stackitcloud/stackit-cli/internal/pkg/args"
	"github.com/stackitcloud/stackit-cli/internal/pkg/utils"
)

func NewCmd(params *params.CmdParams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routing-table",
		Short: "Manage routing-tables and its according routes",
		Long:  "Manage routing-tables and its according routes",
		Args:  args.NoArgs,
		Run:   utils.CmdHelp,
	}
	addSubcommands(cmd, params)
	return cmd
}

func addSubcommands(cmd *cobra.Command, params *params.CmdParams) {
	cmd.AddCommand(
		rtList.NewCmd(params),
		rtDescribe.NewCmd(params),
		route.NewCmd(params),
	)
}

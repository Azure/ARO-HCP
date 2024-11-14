package inspect

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	output "github.com/Azure/ARO-HCP/tooling/templatize/internal/utils"
)

func NewCommand() *cobra.Command {
	opts := options.DefaultRolloutOptions()

	format := "json"
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "inspect",
		Long:  "inspect",
		RunE: func(cmd *cobra.Command, args []string) error {
			return dumpConfig(format, opts)
		},
	}
	if err := options.BindRolloutOptions(opts, cmd); err != nil {
		log.Fatal(err)
	}
	cmd.Flags().StringVar(&format, "format", format, "output format (json, yaml)")
	return cmd
}

func dumpConfig(format string, opts *options.RawRolloutOptions) error {
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}

	var dumpFunc func(interface{}) (string, error)
	switch format {
	case "json":
		dumpFunc = output.PrettyPrintJSON
	case "yaml":
		dumpFunc = output.PrettyPrintYAML
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
	data, err := dumpFunc(completed.Config)
	if err != nil {
		return err
	}
	fmt.Println(data)
	return nil
}

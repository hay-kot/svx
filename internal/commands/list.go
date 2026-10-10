package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
)

// ListCmd implements the list command.
type ListCmd struct {
	flags *Flags
}

// NewListCmd creates a new list command.
func NewListCmd(flags *Flags) *ListCmd {
	return &ListCmd{flags: flags}
}

// Register adds the list command to the application.
func (cmd *ListCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:  "list",
		Usage: "list command",
		Flags: []cli.Flag{
			// Add command-specific flags here
		},
		Action: cmd.run,
	})

	return app
}

func (cmd *ListCmd) run(_ context.Context, _ *cli.Command) error {
	log.Info().Msg("running list command")
	if cmd.flags.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{
			"message": "Hello World!",
		})
	}

	fmt.Println("Hello World!")

	return nil
}

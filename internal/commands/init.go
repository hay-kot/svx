package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
)

// InitCmd implements the init command.
type InitCmd struct {
	flags *Flags
}

// NewInitCmd creates a new init command.
func NewInitCmd(flags *Flags) *InitCmd {
	return &InitCmd{flags: flags}
}

// Register adds the init command to the application.
func (cmd *InitCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:  "init",
		Usage: "init command",
		Flags: []cli.Flag{
			// Add command-specific flags here
		},
		Action: cmd.run,
	})

	return app
}

func (cmd *InitCmd) run(_ context.Context, _ *cli.Command) error {
	log.Info().Msg("running init command")
	if cmd.flags.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{
			"message": "Hello World!",
		})
	}

	fmt.Println("Hello World!")

	return nil
}

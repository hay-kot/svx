package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
)

// UpCmd implements the up command.
type UpCmd struct {
	flags *Flags
}

// NewUpCmd creates a new up command.
func NewUpCmd(flags *Flags) *UpCmd {
	return &UpCmd{flags: flags}
}

// Register adds the up command to the application.
func (cmd *UpCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:  "up",
		Usage: "up command",
		Flags: []cli.Flag{
			// Add command-specific flags here
		},
		Action: cmd.run,
	})

	return app
}

func (cmd *UpCmd) run(_ context.Context, _ *cli.Command) error {
	log.Info().Msg("running up command")
	if cmd.flags.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{
			"message": "Hello World!",
		})
	}

	fmt.Println("Hello World!")

	return nil
}

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
)

// DoctorCmd implements the doctor command.
type DoctorCmd struct {
	flags *Flags
}

// NewDoctorCmd creates a new doctor command.
func NewDoctorCmd(flags *Flags) *DoctorCmd {
	return &DoctorCmd{flags: flags}
}

// Register adds the doctor command to the application.
func (cmd *DoctorCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:  "doctor",
		Usage: "doctor command",
		Flags: []cli.Flag{
			// Add command-specific flags here
		},
		Action: cmd.run,
	})

	return app
}

func (cmd *DoctorCmd) run(_ context.Context, _ *cli.Command) error {
	log.Info().Msg("running doctor command")
	if cmd.flags.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{
			"message": "Hello World!",
		})
	}

	fmt.Println("Hello World!")

	return nil
}

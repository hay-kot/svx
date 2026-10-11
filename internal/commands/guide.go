package commands

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

//go:embed guide.md
var guide string

// GuideCmd implements the guide command.
type GuideCmd struct{}

// NewGuideCmd creates a new guide command.
func NewGuideCmd() *GuideCmd {
	return &GuideCmd{}
}

// Register adds the guide command to the application.
func (cmd *GuideCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:  "guide",
		Usage: "print the guide to writing svx.yaml, for people and coding agents",
		Action: func(context.Context, *cli.Command) error {
			_, err := fmt.Fprint(os.Stdout, guide)
			return err
		},
	})
	return app
}

package app

import (
	"context"
	"github.com/Sanchous98/go-di/v2"
	"github.com/spf13/cobra"
)

var a app

func init() { a = newApp() }

type app struct {
	di.Runner
	cobra.Command
}

func newApp() app {
	return app{
		Runner: di.NewApplication("php"),
	}
}

func (a *app) RootCommand() *cobra.Command { return &a.Command }
func (a *app) Name() string                { return a.Runner.Name() }
func (a *app) Context() context.Context    { return a.Command.Context() }
func (a *app) Run(ctx context.Context)     { a.Runner.Run(ctx) }

type Runner interface {
	di.Container

	AddCommand(...*cobra.Command)
	RootCommand() *cobra.Command
	Name() string
	Context() context.Context
	ExecuteContext(context.Context) error
}

func App() Runner { return &a }

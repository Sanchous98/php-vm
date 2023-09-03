package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"os"
	"php-vm/internal/app"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
)

func init() {
	app.App().AddCommand(&cobra.Command{
		Use: "run",
		Run: func(cmd *cobra.Command, args []string) {
			comp := app.App().Get((*compiler.Compiler)(nil)).(*compiler.Compiler)
			file, err := os.Open(args[0])

			if err != nil {
				panic(err)
			}

			defer file.Close()

			ctx := new(vm.GlobalContext)
			input, _ := io.ReadAll(file)
			fn := comp.Compile(input, ctx)
			res := ctx.Run(fn)

			if res != nil {
				fmt.Fprintln(cmd.OutOrStdout(), res)
			}
		},
	})
}

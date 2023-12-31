package cli

import (
	"context"
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

			parent, cancel := context.WithCancel(context.Background())
			ctx := vm.NewGlobalContext(parent, cmd.InOrStdin(), cmd.OutOrStdout())
			input, _ := io.ReadAll(file)
			fn := comp.Compile(input, &ctx)
			ctx.Run(fn)
			cancel()
		},
	})

	app.App().AddCommand(&cobra.Command{
		Use: "dump",
		Run: func(cmd *cobra.Command, args []string) {
			comp := app.App().Get((*compiler.Compiler)(nil)).(*compiler.Compiler)
			file, err := os.Open(args[0])

			if err != nil {
				panic(err)
			}

			defer file.Close()

			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := vm.NewGlobalContext(parent, cmd.InOrStdin(), cmd.OutOrStdout())
			input, _ := io.ReadAll(file)
			fn := comp.Compile(input, &ctx)

			fmt.Printf("main(args=%d, vars=%d)", fn.Args, fn.Vars)
			fmt.Println(fn.Instructions.String())

			for i, f := range ctx.Functions {
				switch f := f.(type) {
				case vm.CompiledFunction:
					fmt.Printf("%d(args=%d, vars=%d)", i, f.Args, f.Vars)
					fmt.Println(f.Instructions.String())
				}
			}
		},
	})
}

package repl

import (
	"bufio"
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"os"
	"php-vm/internal/app"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
)

const Prompt = ">> "

func start(in io.Reader, out io.Writer, comp *compiler.Compiler) {
	scanner := bufio.NewScanner(in)
	fmt.Printf(Prompt)

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := vm.NewGlobalContext(parent, in, out)

	for scanner.Scan() {
		line := scanner.Bytes()
		fn := comp.Compile(line, ctx)
		ret := ctx.Run(fn)
		if ret != nil {
			fmt.Fprintln(out, ret)
		}

		fmt.Printf(Prompt)
	}
}

func init() {
	app.App().AddCommand(&cobra.Command{
		Use:   "shell",
		Short: "sh",
		Run: func(cmd *cobra.Command, args []string) {
			comp := app.App().Get((*compiler.Compiler)(nil)).(*compiler.Compiler)
			start(os.Stdin, os.Stdout, comp)
		},
	})
}

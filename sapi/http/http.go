package http

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"net/http"
	"os"
	"php-vm/internal/app"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"time"
)

func init() {
	cmd := &cobra.Command{
		Use: "http",
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := cmd.Flags().GetString("addr")

			if err != nil {
				return err
			}
			fmt.Printf("Listening %s\n", addr)

			processLimiter := make(chan struct{}, 120)

			return http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				processLimiter <- struct{}{}
				defer func() { <-processLimiter }()
				comp := app.App().Get((*compiler.Compiler)(nil)).(*compiler.Compiler)
				file, err := os.Open("index.php")

				if err != nil {
					panic(err)
				}

				input, _ := io.ReadAll(file)
				file.Close()

				parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				ctx := vm.NewGlobalContext(parent, nil, w)
				fn := comp.Compile(input, ctx)
				ctx.Run(fn)
			}))
		},
	}
	cmd.PersistentFlags().String("addr", ":80", "")
	app.App().AddCommand(cmd)
}

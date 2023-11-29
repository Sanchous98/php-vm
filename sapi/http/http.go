package http

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"net"
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

			srv := &http.Server{
				ConnContext: func(parent context.Context, c net.Conn) context.Context {
					ctx, _ := context.WithTimeout(parent, 30*time.Second)
					return ctx
				},
				Addr: addr,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					processLimiter <- struct{}{}
					defer func() { <-processLimiter }()

					comp := app.App().Get((*compiler.Compiler)(nil)).(*compiler.Compiler)
					file, err := os.Open("index.php")

					if err != nil {
						panic(err)
					}

					input, _ := io.ReadAll(file)
					_ = file.Close()

					ctx := vm.NewGlobalContext(r.Context(), nil, w)
					fn := comp.Compile(input, &ctx)
					ctx.Run(fn)
				}),
			}

			return srv.ListenAndServe()
		},
	}
	cmd.PersistentFlags().String("addr", ":80", "")
	app.App().AddCommand(cmd)
}

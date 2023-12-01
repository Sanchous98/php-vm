package main

import (
	"context"
	"log"
	//_ "php-vm/ext"
	"php-vm/internal/app"
	_ "php-vm/sapi/cli"
	_ "php-vm/sapi/fcgi"
	_ "php-vm/sapi/http"
	_ "php-vm/sapi/repl"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		if err := recover(); err != nil {
			cancel()
			log.Fatal(err)
		}
	}()

	if err := app.App().ExecuteContext(ctx); err != nil {
		log.Fatal(err)
	}
}

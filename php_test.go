package main

import (
	"context"
	"io"
	"os"
	"php-vm/internal/app"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"testing"
)

func BenchmarkPhp(b *testing.B) {
	comp := app.App().Get((*compiler.Compiler)(nil)).(*compiler.Compiler)
	file, err := os.Open("index.php")

	if err != nil {
		panic(err)
	}

	defer file.Close()

	ctx := vm.NewGlobalContext(context.Background(), os.Stdin, os.Stdout)
	input, _ := io.ReadAll(file)
	fn := comp.Compile(input, ctx)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Run(fn)
	}
}

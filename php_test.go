package main

import (
	"io"
	"os"
	"php-vm/internal/app"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"testing"
)

func Benchmark_main(b *testing.B) {
	file, err := os.Open("index.php")

	if err != nil {
		panic(err)
	}

	defer file.Close()

	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		comp := app.App().Get((*compiler.Compiler)(nil)).(*compiler.Compiler)
		ctx := new(vm.GlobalContext)
		input, _ := io.ReadAll(file)
		fn := comp.Compile(input, ctx)
		res := ctx.Run(fn)
		if res != vm.Int(55) {
			panic(res)
		}
		comp.Reset()
	}
}

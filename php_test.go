package main

import (
	"context"
	"io"
	"os"
	"php-vm/ext/std"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"testing"
)

func BenchmarkPhp(b *testing.B) {
	file, err := os.Open("index.php")

	if err != nil {
		panic(err)
	}

	input, _ := io.ReadAll(file)
	file.Close()

	comp := compiler.NewCompiler(&compiler.Extensions{Exts: []compiler.Extension{std.Ext}})
	ctx := vm.NewGlobalContext(context.Background(), nil, nil)
	fn := comp.Compile(input, ctx)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Run(fn)
	}
}

package php

import (
	"fmt"
	"os"
	"testing"
)

// TestScratchDisasm печатает байткод сниппета из PHP_SNIPPET (отладка).
func TestScratchDisasm(t *testing.T) {
	src := os.Getenv("PHP_SNIPPET")
	if src == "" {
		t.Skip("PHP_SNIPPET не задан")
	}
	c, err := Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("=== main ===")
	fmt.Print(c.Main.Disassemble())
	for _, f := range c.Funcs {
		fmt.Println("=== func", f.Name, "===")
		if cf, ok := f.Executable.(interface{ Disassemble() string }); ok {
			fmt.Print(cf.Disassemble())
		}
	}
}

package php

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"phpvm/internal/vm"
)

// TestScratchAgent выполняет PHP-код из SCRATCH_PHP (или файла SCRATCH_FILE) и
// печатает вывод. Отладочный инструмент, не для CI.
func TestScratchAgent(t *testing.T) {
	src := os.Getenv("SCRATCH_PHP")
	name := "t.php"
	if f := os.Getenv("SCRATCH_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src = string(b)
		name = f
	}
	if src == "" {
		t.Skip("SCRATCH_PHP/SCRATCH_FILE не задан")
	}
	if os.Getenv("SCRATCH_LEX") != "" {
		toks, err := Lex(src)
		if err != nil {
			fmt.Printf("--- LEX ERROR: %v\n", err)
			return
		}
		for _, tok := range toks {
			fmt.Printf("%-8v %-12q attrs=%v\n", tok.Kind, tok.Val, tok.Attrs)
		}
		return
	}
	if os.Getenv("SCRATCH_PARSE") != "" {
		prog, err := Parse(src)
		if err != nil {
			fmt.Printf("--- PARSE ERROR: %v\n", err)
			return
		}
		for _, st := range prog.Stmts {
			switch x := st.(type) {
			case *FuncDecl:
				fmt.Printf("func %s ret=%q\n", x.Name, x.RetType)
			case *ClassDecl:
				fmt.Printf("class %s\n", x.Name)
				for _, m := range x.Members {
					if m.Kind == "method" {
						fmt.Printf("  method %s ret=%q\n", m.MName, m.RetType)
					}
				}
			}
		}
		return
	}
	if os.Getenv("SCRATCH_INI") != "" {
		SetIni(map[string]string{"display_errors": os.Getenv("SCRATCH_INI")})
		defer SetIni(nil)
	}
	c, err := CompileFile(src, name)
	if err != nil {
		fmt.Printf("--- COMPILE ERROR: %v\n", err)
		return
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetStepLimit(5000000)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	err = m.Run()
	fmt.Printf("--- OUT ---\n%s\n--- END (run err=%v)\n", buf.String(), err)
}

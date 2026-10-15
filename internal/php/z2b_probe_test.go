package php

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestZ2BProbe — инструмент разработки агента z2b: печатает сырой GOT и WANT
// (и выход живого php) для одного .phpt из PHPT_FILE.
func TestZ2BProbe(t *testing.T) {
	f := os.Getenv("PHPT_FILE")
	if f == "" {
		t.Skip("PHPT_FILE не задан")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	got, rerr := runPhptAt(p, f)
	fmt.Printf("\n@@@@@ %s\n---- GOT ----\n%s\n---- ERR: %v\n---- WANT ----\n%s\n", f, got, rerr, p.Expect+p.Expectf)
	if strings.TrimSpace(got) != "" {
		for i, ln := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
			fmt.Printf("G%03d|%s\n", i+1, ln)
		}
	}
	if real, rerr := runRealPHP(p); rerr == nil {
		fmt.Printf("---- vsPHP ----\n%s\n", real)
	} else {
		fmt.Printf("---- vsPHP err: %v\n", rerr)
	}
}

// TestZ2BSnippet — прогон сырого кода из файла PHPT_SNIPPET (отладка).
func TestZ2BSnippet(t *testing.T) {
	f := os.Getenv("PHPT_SNIPPET")
	if f == "" {
		t.Skip("PHPT_SNIPPET не задан")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	got, rerr := runPhptAt(PhpT{File: string(data)}, f)
	fmt.Printf("\n@@@@@ SNIPPET\n%s---- GOT ----\n%s\n---- ERR: %v\n", string(data), got, rerr)
}

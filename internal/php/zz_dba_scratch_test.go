package php

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestZZDBAScratch — временный хелпер триажа (удаляется перед коммитом).
// PHPT_FILE=... печатает полный GOT и построчный diff с выводом живого php.
func TestZZDBAScratch(t *testing.T) {
	f := os.Getenv("PHPT_FILE")
	if f == "" {
		t.Skip("PHPT_FILE не задан")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	got, _ := runPhptAt(p, f)
	g := stripStartup(strings.TrimSpace(got))
	fmt.Printf("\n===== GOT =====\n%s\n", g)
	real, rerr := runRealPHP(p)
	_ = rerr
	r := strings.TrimSpace(stripStartup(strings.ReplaceAll(real, "\r\n", "\n")))
	if m := regexp.MustCompile(`/tmp/agentphp-[0-9]+\.php`).FindString(r); m != "" {
		r = strings.ReplaceAll(r, m, f[:len(f)-len(".phpt")]+".php")
	}
	r = strings.ReplaceAll(r, "PHP Warning:  ", "Warning: ")
	r = strings.ReplaceAll(r, "PHP Fatal error:  ", "Fatal error: ")
	r = strings.ReplaceAll(r, "PHP Notice:  ", "Notice: ")
	r = strings.ReplaceAll(r, "PHP Deprecated:  ", "Deprecated: ")
	r = strings.ReplaceAll(r, "\nPHP Warning:", "\nWarning:")
	r = strings.ReplaceAll(r, "\nPHP Notice:", "\nNotice:")
	r = strings.ReplaceAll(r, "\nPHP Deprecated:", "\nDeprecated:")
	r = strings.ReplaceAll(r, "\nPHP Fatal error:", "\nFatal error:")
	fmt.Printf("===== PHP =====\n%s\n", r)
	gl := strings.Split(g, "\n")
	rl := strings.Split(r, "\n")
	n := len(gl)
	if len(rl) > n {
		n = len(rl)
	}
	for i := 0; i < n; i++ {
		var a, b string
		if i < len(gl) {
			a = gl[i]
		}
		if i < len(rl) {
			b = rl[i]
		}
		if a != b {
			fmt.Printf("DIFF %2d:\n  got=%q\n  php=%q\n", i+1, a, b)
		}
	}
}

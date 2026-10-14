package php

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

func runCompiled(t *testing.T, src string) (string, error) {
	t.Helper()
	c, err := Compile(src)
	if err != nil {
		return "", err
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	err = m.Run()
	return buf.String(), err
}

func TestCompileVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		`<?php echo 1+2*3;`,
		`<?php $a=10; $b=20; echo $a+$b;`,
		`<?php echo 2<3;`,
		`<?php echo (1+2)*(3+4);`,
		`<?php echo 10-3*2;`,
		`<?php echo "a"."b"."c";`,
		`<?php $i=0; while($i<5){ echo $i; $i=$i+1; }`,
		`<?php if (1<2) { echo "y"; } else { echo "n"; }`,
		`<?php function add($a,$b){ return $a+$b; } echo add(2,3);`,
		`<?php function f($n){ if($n<2){ return 1; } return $n*f($n-1); } echo f(5);`,
	}
	for _, src := range cases {
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			t.Fatalf("php %q: %v", src, werr)
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q: run error: %v", src, err)
			continue
		}
		if got != string(want) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}

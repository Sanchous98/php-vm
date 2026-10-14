package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRefsAgentVsPHP — дифференциальные кейсы ссылочной семантики и маркера
// `&` в var_dump: foreach по ссылке (массивы/объекты), присваивание по ссылке
// в элемент/свойство, литералы массива со ссылками, типизированные свойства,
// unset и разрыв ссылки. Сравнение с живым php 8.3.
func TestRefsAgentVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// foreach по ссылке: ссылкой остаётся только последний элемент.
		`<?php $a=[1,2,3]; foreach($a as &$v){ $v*=10; } var_dump($a);`,
		`<?php $a=["x"=>1,"y"=>2,"z"=>3]; foreach($a as $k=>&$v){ $v=$k; } var_dump($a);`,
		`<?php $a=[[1,2],[3,4]]; foreach($a as &$r){ $r[]=9; } var_dump($a);`,
		// foreach по свойствам объекта.
		`<?php class C{ public $a=1; public $b=2; public $c=3; } $o=new C; foreach($o as $k=>&$v){ $v*=10; } var_dump($o);`,
		// присваивание по ссылке: элемент массива.
		`<?php $a=[1,2]; $x=7; $a[0]=&$x; $x=9; var_dump($a,$x);`,
		`<?php $a=[]; $x=1; $a[]=&$x; $x=2; var_dump($a,$x);`,
		`<?php $a=[1]; $y=&$a[0]; $y=5; var_dump($a,$y);`,
		// присваивание по ссылке: свойство объекта.
		`<?php class T{ public $p=1; } $o=new T; $x=5; $o->p=&$x; $x=6; var_dump($o,$x);`,
		`<?php class T{ public $p=1; } $o=new T; $y=&$o->p; $y=8; var_dump($o,$y);`,
		// литерал массива со ссылкой.
		`<?php $b=1; $a=[&$b]; $b=2; var_dump($a,$b);`,
		`<?php $b=1; $a=["k"=>&$b]; $b=3; var_dump($a);`,
		// типизированное свойство + ссылка (приведение при записи через ячейку).
		`<?php class C{ public string $x="x"; } $o=new C; $y="y"; $o->x=&$y; $y=42; var_dump($o,$y);`,
		`<?php class C{ public int $x=1; } $o=new C; $y=&$o->x; $y=4; var_dump($o,$y);`,
		// write-through: запись в элемент/свойство, являющееся ссылкой.
		`<?php $x=1; $a=[&$x]; $a[0]=9; var_dump($a,$x);`,
		// unset элемента не рвёт цель ссылки.
		`<?php $x=1; $a=[&$x]; unset($a[0]); $x=2; var_dump($a,$x);`,
	}

	for _, src0 := range cases {
		src := src0
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			t.Logf("php error for %q: %v", src, werr)
			continue
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

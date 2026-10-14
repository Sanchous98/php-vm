package php

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// ssaDeterminismSnippets — программы, покрывающие phi-узлы (циклы, ветвления),
// окна вызовов, try/catch (исключительные рёбра) и группировку окон. Если
// аллокатор недетерминирован, повторная компиляция даёт разный байткод.
var ssaDeterminismSnippets = []string{
	`<?php $s=0; for($i=0;$i<10;$i++){ $s=$s+$i; } echo $s;`,
	`<?php function f($n){ if($n<2) return $n; return f($n-1)+f($n-2); } echo f(10);`,
	`<?php $a=1; $b=2; if($a<$b){ $c=$a; } else { $c=$b; } echo $c+$a+$b;`,
	`<?php function g($a,$b,$c){ return $a+$b+$c; } echo g(1,2,3)+g(4,5,6);`,
	`<?php class C { public $v; function __construct($x){ $this->v=$x; } function get(){ return $this->v; } } $o=new C(5); echo $o->get();`,
	`<?php try { throw new Exception("x"); } catch (Exception $e) { echo $e->getMessage(); } finally { echo "!"; }`,
	`<?php $r=0; foreach([1,2,3,4] as $k=>$v){ $r=$r+$k+$v; } echo $r;`,
	`<?php function h(...$a){ return array_sum($a); } echo h(1,2,3,4,5);`,
	`<?php $x=[1,2,3]; $y=[]; foreach($x as $v){ $y[]=$v*2; } echo implode(",",$y);`,
	`<?php switch(3){ case 1: echo "a"; break; case 2: echo "b"; break; case 3: echo "c"; break; default: echo "d"; }`,
	`<?php $s=""; for($i=0;$i<5;$i++){ if($i%2==0){ $s.="e"; } else { $s.="o"; } } echo $s;`,
}

// TestSSADeterminism проверяет, что повторная компиляция одной и той же
// программы даёт побайтово одинаковый байткод (включая раскладку регистров,
// порядок трампов и т.п.).
func TestSSADeterminism(t *testing.T) {
	const reps = 40
	for _, src := range ssaDeterminismSnippets {
		var want string
		for k := 0; k < reps; k++ {
			c, err := Compile(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got := dumpCompiled(c)
			if k == 0 {
				want = got
				continue
			}
			if got != want {
				t.Fatalf("недетерминированный байткод на повторе %d для:\n%s\n--- first ---\n%s\n--- got ---\n%s", k, src, want, got)
			}
		}
	}
}

func dumpCompiled(c *Compiled) string {
	var b strings.Builder
	b.WriteString("== main ==\n")
	b.WriteString(c.Main.Disassemble())
	for i, f := range c.Funcs {
		if cf, ok := f.Executable.(*vm.CompiledFunction); ok {
			b.WriteString("\n== func ")
			b.WriteByte(byte('0' + i%10))
			b.WriteString(" ==\n")
			b.WriteString(cf.Disassemble())
		}
	}
	return b.String()
}

// TestSSADeterminismCorpus проверяет побайтовую воспроизводимость байткода на
// реальных .phpt из PHPT_DIR (если задан). Отдельные файлы, которые не
// компилируются, пропускаются.
func TestSSADeterminismCorpus(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	if root == "" {
		t.Skip("PHPT_DIR не задан")
	}
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	if len(files) > 60 {
		files = files[:60]
	}
	const reps = 6
	checked := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		p := parsePhpt(string(data))
		if p.File == "" {
			continue
		}
		var want string
		bad := false
		for k := 0; k < reps; k++ {
			c, err := CompileFile(p.File, f)
			if err != nil {
				bad = true
				break
			}
			got := dumpCompiled(c)
			if k == 0 {
				want = got
			} else if got != want {
				t.Fatalf("недетерминированный байткод (%s, повтор %d)", f, k)
			}
		}
		if !bad {
			checked++
		}
	}
	t.Logf("детерминизм подтверждён на %d файлах из %s", checked, root)
}

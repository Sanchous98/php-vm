package php

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// phpRunExec выполняет PHP-программу живым php 8.3 (эталон) и возвращает stdout.
func phpRunExec(t *testing.T, src string) string {
	t.Helper()
	cmd := exec.Command("php", "-r", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("php -r упал: %v\nstderr=%s\nsrc=%s", err, errb.String(), src)
	}
	return out.String()
}

// phpRunVM выполняет ту же программу в интерпретаторе. Тело оборачивается в
// PHP-теги: php -r принимает код без тегов, а CompileFile — с ними.
func phpRunVM(t *testing.T, src string) string {
	t.Helper()
	out, err := runFile("<?php\n" + src + "\n?>")
	if err != nil {
		t.Fatalf("phpvm: %v\nsrc=%s", err, src)
	}
	return out
}

// TestSerializeDifferential прогоняет одну и ту же программу в phpvm и в php,
// сравнивая вывод побайтово. Покрывает форматы serialize/unserialize,
// var_export и print_r (в т.ч. объекты).
func TestSerializeDifferential(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"scalars", `echo serialize(NULL),serialize(true),serialize(false),serialize(1),serialize(-1),serialize(0);`},
		{"strings", `echo serialize(""),serialize("hallo"),serialize(chr(0)."a".chr(0)."b");`},
		{"floats-default", `echo serialize(0.1),serialize(-0.0),serialize(1.0),serialize(100.0),serialize(1e2),serialize(1.5);`},
		{"floats-sci", `echo serialize(5.2e25),serialize(85.29e-23),serialize(9e-9),serialize(-1.123456789);`},
		{"floats-special", `echo serialize(INF),serialize(-INF),serialize(NAN);`},
		{"arrays", `echo serialize([]),serialize([1,2,3]),serialize([1,"a"=>[true,null,1.5]]);`},
		{"array-keys", `echo serialize(["10"=>1,"01"=>2,5=>3,-2=>4]);`},
		{"nested-empty", `echo serialize([[],[""],[]]);`},
		{"object-stdclass", `echo serialize((object)["a"=>1]);`},
		{"object-nested", `echo serialize((object)["a"=>[1,(object)["b"=>2]]]);`},
		{"object-roundtrip", `$x=(object)["a"=>1,"b"=>[true,null]]; echo serialize(unserialize(serialize($x)));`},
		{"unserialize-scalars", `var_dump(unserialize('b:0;'));var_dump(unserialize('b:1;'));var_dump(unserialize('i:823;'));var_dump(unserialize('s:0:"";'));var_dump(unserialize('s:3:"foo";'));`},
		{"unserialize-arrays", `var_dump(unserialize('a:2:{i:0;a:0:{}i:1;a:0:{}}'));var_dump(unserialize('a:3:{i:0;s:3:"foo";i:1;s:3:"bar";i:2;s:3:"baz";}'));`},
		{"unserialize-object", `echo serialize(unserialize('O:8:"stdClass":2:{s:2:"aa";i:1;s:2:"bb";i:2;}'));`},
		{"unserialize-numkeys", `var_dump(unserialize('a:2:{s:2:"10";i:1;s:2:"01";i:2;}'));`},
		{"roundtrip-eq", `$x=[1,"a"=>[true,null,1.5]]; var_dump(unserialize(serialize($x))); echo serialize(unserialize(serialize($x)));`},
		{"object-identity", `$x=unserialize('a:3:{i:0;i:5;i:1;O:8:"stdClass":1:{s:1:"a";i:1;}i:2;r:3;}'); echo serialize($x);`},
		{"var-export-scalars", `var_export(NULL);echo "\n";var_export(true);echo "\n";var_export(1.5);echo "\n";var_export("a'b\\c");echo "\n";`},
		{"var-export-arrays", `var_export([1,2,"a"=>[true,null]]);echo "\n";var_export([]);echo "\n";`},
		{"var-export-objs", `var_export((object)["a"=>1]);echo "\n";var_export((object)["a"=>(object)["b"=>2]]);echo "\n";var_export([1,(object)["z"=>"q"],"k"=>[true,null]]);echo "\n";`},
		{"var-export-nested", `var_export((object)["p"=>["a"=>["b"=>(object)["c"=>1]]]]);echo "\n";`},
		{"print-r-array", `print_r([1,2,"a"=>[true,null]]);`},
		{"print-r-object", `print_r((object)["a"=>1,"b"=>[1,2]]);`},
		// Порядок свойств объекта в vm не сохраняется (props — Go-map, а
		// объявленный порядок не трекается) — поэтому имена идут по алфавиту,
		// как их отдаёт objectJSONEntries.
		{"print-r-nested-object", `print_r((object)["a"=>(object)["y"=>2],"b"=>"hi","c"=>null]);`},
		{"print-r-return", `var_dump(print_r([1,2],true));`},
		{"object-self-ref", `$o=(object)["a"=>1]; $o->b=$o; echo serialize($o);`},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			want := phpRunExec(t, c.src)
			got := phpRunVM(t, c.src)
			if got != want {
				t.Errorf("вывод отличается\n--- php  ---\n%q\n--- phpvm ---\n%q", want, got)
			}
		})
	}
}

// TestSerializeRoundTrip проверяет, что unserialize(serialize($x)) даёт равное
// значение (для скаляров/массивов; объекты сравниваются по serialize-строке).
func TestSerializeRoundTrip(t *testing.T) {
	// Сравниваем serialize-строки (round-trip), а не VM-оператор === :
	// глубокое сравнение массивов реализуется не в builtins.go.
	src := `
function chk($x) {
    $s = serialize($x);
    $y = unserialize($s);
    echo (serialize($y) === $s ? "EQ" : "NE"), " ", $s, "\n";
}
chk(null); chk(true); chk(false); chk(0); chk(-7); chk(0.1); chk(-0.0);
chk(""); chk("héllo"); chk([]); chk([1,2,3]);
chk([1,"a"=>[true,null,1.5]]);
chk(["x"=>[], "y"=>["z"=>[1]]]);
`
	want := phpRunExec(t, src)
	got := phpRunVM(t, src)
	if got != want {
		t.Errorf("round-trip отличается\n--- php  ---\n%s\n--- phpvm ---\n%s", want, got)
	}
}

// TestVarExportClass проверяет, что var_export пользовательского объекта даёт
// \Class::__set_state(array(...)), а stdClass — (object) array(...).
func TestVarExportClass(t *testing.T) {
	src := `
class Foo { public $a = 1; }
var_export(new Foo);
echo "\n";
echo var_export((object)["z" => 2], true), "\n";
echo var_export((object)["a" => (object)["b" => [1, 2]]]), "\n";
`
	want := phpRunExec(t, src)
	got := phpRunVM(t, src)
	if got != want {
		t.Errorf("var_export класса отличается\n--- php  ---\n%s\n--- phpvm ---\n%s", want, got)
	}
	if !strings.Contains(got, `\Foo::__set_state(array(`) {
		t.Errorf("нет \\Foo::__set_state в выводе: %q", got)
	}
}

// TestUnserializeRejectsBroken: битый вход даёт false без паники.
func TestUnserializeRejectsBroken(t *testing.T) {
	broken := []string{
		"s:-1:\"\";", "i:823", "O:8:\"stdClass :0:{}", "O:8:\"stdClass\"+0:{}",
		"a:2:{i:0;s:2:\"12\":", "a:2:{i:0;s:2:\"12\";i:1;s:3000:\"123",
		"s:3000:\"123\";", "s:3:\"123;", "R:1;", "r:1;", "a:1:{", "d:abc;", "b:2;",
	}
	for _, in := range broken {
		src := `$v = @unserialize(` + phpQuote(in) + `); var_dump($v);`
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("паника на входе %q: %v", in, r)
				}
			}()
			got := strings.TrimSpace(phpRunVM(t, src))
			if got != "bool(false)" {
				t.Errorf("вход %q: получено %q, ожидалось bool(false)", in, got)
			}
		}()
	}
}

func phpQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '$':
			b.WriteString(`\$`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}

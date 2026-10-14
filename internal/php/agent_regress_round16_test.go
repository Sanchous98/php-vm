package php

import (
	"strings"
	"testing"
)

// runSrcAgent выполняет сниппет с чистой таблицей ресурсов (как отдельный
// .phpt-прогон) и возвращает вывод.
func runSrcAgent(t *testing.T, src string) string {
	t.Helper()
	ResetResourceState()
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v (out=%q)", err, out)
	}
	return out
}

// TestProcOpenDescriptorSpecAgent — регрессии разбора descriptor_spec:
// диагностики Zend-совместимы, $pipes не меняется при ошибке.
func TestProcOpenDescriptorSpecAgent(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "invalid spec/mode",
			src:  `<?php var_dump(proc_open(["/bin/cat"], [1=>["pi"]], $pipes)); echo "END\n";`,
			want: []string{"pi is not a valid descriptor spec/mode", "bool(false)", "END"},
		},
		{
			name: "scalar item",
			src: `<?php try { proc_open(["/bin/cat"], [1=>1], $pipes); } catch (ValueError $e) {
				echo $e->getMessage(), "\n"; }`,
			want: []string{"proc_open(): Argument #2 ($descriptor_spec) must only contain arrays and streams"},
		},
		{
			name: "missing redirect target",
			src: `<?php try { proc_open(["/bin/cat"], [1=>["redirect"]], $pipes); } catch (ValueError $e) {
				echo $e->getMessage(), "\n"; }`,
			want: []string{"Missing redirection target"},
		},
		{
			name: "redirect type",
			src: `<?php try { proc_open(["/bin/cat"], [1=>["redirect","x"]], $pipes); } catch (ValueError $e) {
				echo $e->getMessage(), "\n"; }`,
			want: []string{"Redirection target must be of type int, string given"},
		},
		{
			name: "redirect not found",
			src:  `<?php var_dump(proc_open(["/bin/cat"], [1=>["redirect",42]], $pipes));`,
			want: []string{"Redirection target 42 not found", "bool(false)"},
		},
		{
			name: "missing file name",
			src: `<?php try { proc_open(["/bin/cat"], [1=>["file"]], $pipes); } catch (ValueError $e) {
				echo $e->getMessage(), "\n"; }`,
			want: []string{"Missing file name parameter for 'file'"},
		},
		{
			name: "missing pipe mode",
			src: `<?php try { proc_open(["/bin/cat"], [1=>["pipe"]], $pipes); } catch (ValueError $e) {
				echo $e->getMessage(), "\n"; }`,
			want: []string{"Missing mode parameter for 'pipe'"},
		},
		{
			name: "command null byte",
			src: `<?php try { proc_open(["php\0oops"], [1=>["null"]], $pipes); } catch (ValueError $e) {
				echo $e->getMessage(), "\n"; }`,
			want: []string{"Command array element 1 contains a null byte"},
		},
		{
			name: "empty program",
			src: `<?php try { proc_open([""], [1=>["null"]], $pipes); } catch (ValueError $e) {
				echo $e->getMessage(), "\n"; }`,
			want: []string{"First element must contain a non-empty program name"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runSrcAgent(t, tc.src)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Fatalf("вывод %q не содержит %q", out, w)
				}
			}
		})
	}
}

// TestProcOpenResourceModelAgent — нумерация ресурсов и уничтожение процесса:
// потерянный ресурс процесса закрывает пайпы (Unknown), как
// proc_open_rsrc_dtor; при живом ресурсе пайпы читаются.
func TestProcOpenResourceModelAgent(t *testing.T) {
	out := runSrcAgent(t, `<?php
$spec = [];
for ($i = 3; $i <= 5; $i++) $spec[$i] = ["pipe", "w"];
proc_open("/bin/cat", $spec, $pipes);
var_dump($pipes);
`)
	if !strings.Contains(out, "resource(4) of type (Unknown)") ||
		!strings.Contains(out, "resource(6) of type (Unknown)") {
		t.Fatalf("пайпы уничтоженного процесса должны быть Unknown: %q", out)
	}

	out = runSrcAgent(t, `<?php
$p = proc_open(["/bin/sh", "-c", 'printf %s "$ENV_A"'], [1 => ["pipe", "w"]], $pipes, null, ["ENV_A" => "XX"]);
var_dump(stream_get_contents($pipes[1]));
$p = 1;
`)
	if !strings.Contains(out, `string(2) "XX"`) {
		t.Fatalf("среда ребёнка должна заменяться: %q", out)
	}

	out = runSrcAgent(t, `<?php
$p = proc_open(["/bin/sh", "-c", "echo OUT; echo ERR >&2"], [1 => ["null"], 2 => ["pipe", "w"]], $pipes);
var_dump($pipes);
var_dump(stream_get_contents($pipes[2]));
proc_close($p);
`)
	if !strings.Contains(out, "resource(4) of type (stream)") || !strings.Contains(out, "string(4) \"ERR") {
		t.Fatalf("null+pipe: ожидался ресурс 4 и ERR: %q", out)
	}
}

// TestAttrLazyArgsAgent — аргументы атрибутов вычисляются отложенно в области
// объявления: forward-константы и self::.
func TestAttrLazyArgsAgent(t *testing.T) {
	out := runSrcAgent(t, `<?php
class C {
	#[A1(1, X)] public const A = 1, B = 2;
	#[A1(self::class, self::FOO)] public function m() { }
	private const FOO = "bar";
}
const X = 2;
foreach (["A", "B"] as $c) {
	var_dump((new ReflectionClassConstant("C", $c))->getAttributes()[0]->getArguments());
}
var_dump((new ReflectionMethod("C", "m"))->getAttributes()[0]->getArguments());
`)
	for _, w := range []string{"int(1)", "int(2)", `string(1) "C"`, `string(3) "bar"`} {
		if !strings.Contains(out, w) {
			t.Fatalf("ленивые аргументы атрибутов: %q не содержит %q", out, w)
		}
	}
}

// TestAttrParserDiagnosticsAgent — compile-time диагностики атрибутов.
func TestAttrParserDiagnosticsAgent(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"call in arg", `<?php #[A1(foo())] class C1 { }`, "Constant expression contains invalid operations"},
		{"dynamic class ref", `<?php #[Attr(a->b::c)] function test() {}`, "Dynamic class names are not allowed in compile-time class constant references"},
		{"variable name", `<?php #[$x] class A {}`, `syntax error, unexpected variable "$x"`},
		{"unpacking", `<?php #[MyAttribute(...[1, 2, 3])] class Foo { }`, "Cannot use unpacking in attribute argument list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runSrcAgent(t, tc.src)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("вывод %q не содержит %q", out, tc.want)
			}
		})
	}
}

// TestListAsArrayParseErrorAgent — list() без присваивания отклоняется на
// разборе (Zend), а list() в foreach/вложенный работает.
func TestListAsArrayParseErrorAgent(t *testing.T) {
	out := runSrcAgent(t, `<?php var_dump(list(1, 2, 3));`)
	if !strings.Contains(out, `syntax error, unexpected token ")", expecting "="`) {
		t.Fatalf("ожидался Parse error: %q", out)
	}

	out = runSrcAgent(t, `<?php
foreach ([[1,2],[3,4]] as list(list($a,$b),$c)) { }
list($x, $y) = [7, 8];
var_dump($x + $y);
`)
	if !strings.Contains(out, "int(15)") {
		t.Fatalf("вложенный list/foreach сломан: %q", out)
	}
}

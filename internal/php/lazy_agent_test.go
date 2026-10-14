package php

import "testing"

// TestLazyClassResolution проверяет, что имена классов разрешаются в рантайме:
// класс/интерфейс/трейт могут быть объявлены позже в том же файле, а также
// использоваться через `::`, `instanceof`, `catch` и `X::class`.
func TestLazyClassResolution(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"forward_extends", `<?php class C extends D { function f(){} } class D { function g(){} } var_dump(new C instanceof D);`, "bool(true)\n"},
		{"forward_implements", `<?php class C implements I { function m(){} } interface I { function m(); } echo "ok";`, "ok"},
		{"forward_trait", `<?php class C { use T; } trait T { function f(){echo "t";} } (new C)->f();`, "t"},
		{"forward_const", `<?php echo C::K; class C { const K = 7; }`, "7"},
		{"forward_staticcall", `<?php echo C::f(); class C { static function f(){return 9;} }`, "9"},
		{"forward_class_keyword", `<?php echo X::class; class X {}`, "X"},
		{"forward_catch", `<?php try { throw new E; } catch (E $e) { echo "caught"; } class E extends Exception {}`, "caught"},
		{"forward_instanceof_str", `<?php $o = new C; var_dump($o instanceof D); class C {} class D {}`, "bool(false)\n"},
	}
	for _, c := range cases {
		got, err := runFile(c.src)
		if err != nil {
			t.Errorf("%s: err=%v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// TestDynamicEvalInFunction проверяет, что eval с нелитеральным аргументом
// внутри функции линкует объявленные классы/функции в общие таблицы VM.
func TestDynamicEvalInFunction(t *testing.T) {
	src := `<?php
$f = function() { $s = 'class Dyn { function g(){ return 11; } }'; eval($s); };
$f();
var_dump(class_exists("Dyn"));
echo (new Dyn)->g();
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("dynamic eval: %v", err)
	}
	if got != "bool(true)\n11" {
		t.Errorf("got %q", got)
	}
}

// TestParseErrorRendering проверяет, что синтаксические ошибки формируются в
// PHP-совместимом виде ("syntax error, unexpected ...", "Unclosed ...").
func TestParseErrorRendering(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"assign_const", `aclass::myConst = "no";`, `syntax error, unexpected token "="`},
		{"amp_at_stmt", `+&`, `syntax error, unexpected token "&"`},
		{"prop_list", "class Foo {\n    public $bar,\n           int $qux;\n}", `syntax error, unexpected identifier "int", expecting variable`},
		{"nullable_intersection", `function foo(): ?Countable&Iterator {}`, `syntax error, unexpected token "&", expecting "{"`},
		{"mixed_cast", `$foo = (mixed) 12;`, `syntax error, unexpected integer "12"`},
		{"static_prop_type", "class T {\n    public ?static $foo;\n}", `syntax error, unexpected token "static"`},
		{"unclosed_paren", `if(1 > 2`, `Unclosed '('`},
		{"unmatched_paren", `(1 + 2));`, `Unmatched ')'`},
		{"mismatch", "(1 +\n  2])", `Unclosed '(' on line 1 does not match ']'`},
	}
	for _, c := range cases {
		_, err := Parse(c.src)
		pe, ok := err.(*parseError)
		if !ok {
			t.Errorf("%s: err=%v (not a parseError)", c.name, err)
			continue
		}
		if pe.msg != c.want {
			t.Errorf("%s: got %q want %q", c.name, pe.msg, c.want)
		}
	}
}

// TestEvalParseErrorThrown проверяет, что eval() сообщает ошибку разбора как
// перехватываемое исключение ParseError с текстом в формате PHP.
func TestEvalParseErrorThrown(t *testing.T) {
	src := "<?php\ntry {\n    eval('this is not valid php (');\n} catch (ParseError $e) {\n    echo get_class($e), ': ', $e->getMessage();\n}\n"
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	// PHP сообщает об ошибке на "is" ещё до непарной скобки в конце.
	want := "ParseError: syntax error, unexpected identifier \"is\""
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

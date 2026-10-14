package php

import (
	"strings"
	"testing"
)

// TestConstExprLazy проверяет отложенные (ленивые) константные выражения:
// ссылки на другие константы, рантайм-define(), магические константы, ::class,
// массивы с константными ключами, дефолты свойств/статиков и трейты.
func TestConstExprLazy(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"self-ref", `<?php class C { const A = 1; const B = self::A + 1; } echo C::B;`, "2"},
		{"parent-ref", `<?php class A { const X = 2; } class B extends A { const Y = parent::X + 1; } echo B::Y;`, "3"},
		{"interface-ref", `<?php interface I { const X = 1; } class B implements I { const Y = self::X + 1; } echo B::Y;`, "2"},
		{"iface-inherit", `<?php interface I { const X2 = 'X' . self::Y2; const Y2 = 'Y'; } class B implements I{} echo B::X2;`, "XY"},
		{"define", `<?php define('D', 5); class C { const X = D; } echo C::X;`, "5"},
		{"define-array-key", `<?php define('FOO1', 1); class A { public $a = array(FOO1=>'x'); } echo (new A)->a[1];`, "x"},
		{"magic-class", `<?php class C { const A = __CLASS__; } echo C::A;`, "C"},
		{"magic-method-empty", `<?php class C { const M = __METHOD__; const F = __FUNCTION__; } echo "[", C::M, C::F, "]";`, "[]"},
		{"magic-line", `<?php class C { const L = __LINE__; } echo C::L;`, "1"},
		{"self-class", `<?php class C { public function m(){ return self::class; } } echo (new C)->m();`, "C"},
		{"static-class", `<?php class C { public function m(){ return static::class; } } echo (new C)->m();`, "C"},
		{"named-class", `<?php class C {} echo C::class;`, "C"},
		{"named-class-unknown", `<?php echo Foo\Bar\Baz::class;`, `Foo\Bar\Baz`},
		{"concat", `<?php class C { const X = 'a' . 'b'; } echo C::X;`, "ab"},
		{"ternary-coalesce", `<?php class C { const X = 1 ? 'y' : 'n'; const Y = null ?? 5; } echo C::X, C::Y;`, "y5"},
		{"array-index", `<?php class A { const AA = [1,2,[3]]; } class C { const B = A::AA[2][0]; } echo C::B;`, "3"},
		{"trait-method-class", `<?php trait T { public function m(){ return __CLASS__; } } class C { use T; } echo (new C)->m();`, "C"},
		{"trait-indirect-class", `<?php trait T { public function m(){ return __CLASS__; } } trait U { use T; } class C { use U; } echo (new C)->m();`, "C"},
		{"trait-static-class", `<?php trait T { public static $v = __CLASS__; } class C { use T; } echo C::$v;`, "C"},
		{"trait-prop-class", `<?php trait T { public $v = __CLASS__; } class C { use T; } echo (new C)->v;`, "C"},
		{"trait-static-indirect", `<?php trait T { public static $v = __CLASS__; } trait U { use T; } class C { use U; } echo C::$v;`, "C"},
		{"enum-magic-value", `<?php enum E: string { case A = __FILE__; } echo gettype(E::A->value);`, "string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runFile(tc.src)
			if err != nil {
				t.Fatalf("err=%v out=%q", err, out)
			}
			got := strings.TrimSpace(out)
			if got != tc.want {
				t.Fatalf("got %q, want %q (raw=%q)", got, tc.want, out)
			}
		})
	}
}

// TestConstExprInvalid проверяет, что недопустимые константные выражения дают
// PHP-совместимый фатал на этапе компиляции.
func TestConstExprInvalid(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"variable", `<?php class C { const X = $v; }`, "Fatal error: Constant expression contains invalid operations in php on line 1"},
		{"new", `<?php class C { const X = new stdClass; }`, "Fatal error: New expressions are not supported in this context in php on line 1"},
		{"new-prop", `<?php class C { public $p = new stdClass; }`, "Fatal error: New expressions are not supported in this context in php on line 1"},
		{"call", `<?php class C { const X = foo(); }`, "Fatal error: Constant expression contains invalid operations in php on line 1"},
		{"enum-var", `<?php enum Foo: int { case Bar = 1 + $x; }`, "Fatal error: Constant expression contains invalid operations in php on line 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runFile(tc.src)
			if err != nil {
				t.Fatalf("unexpected err=%v out=%q", err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("got %q, want substring %q", out, tc.want)
			}
		})
	}
}

// TestConstExprRuntimeErrors проверяет ошибки времени вычисления: несуществующий
// класс и неопределённая константа внутри отложенного выражения.
func TestConstExprRuntimeErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"class-not-found", `<?php class C { public int $bar = BAR::BAZ; } try { new C(); } catch (Error $e) { echo $e->getMessage(); }`, `Class "BAR" not found`},
		{"undef-global", `<?php class C { const X = UNDEFINED_THING; } try { echo C::X; } catch (Error $e) { echo $e->getMessage(); }`, `Undefined constant "UNDEFINED_THING"`},
		{"class-use-time", `<?php class A { public const self CONST1 = C; } try { define("C", new A()); } catch (Error $e) { echo $e->getMessage(), "\n"; } echo "after";`, "Undefined constant \"C\"\nafter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runFile(tc.src)
			if err != nil {
				t.Fatalf("unexpected err=%v out=%q", err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("got %q, want substring %q", out, tc.want)
			}
		})
	}
}

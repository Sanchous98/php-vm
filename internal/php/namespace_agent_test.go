package php

import (
	"testing"
)

// TestNamespaceVsPHP — дифф-кейсы базовых namespace/use против живого php.
// Использует runVsPHP из syntax_agent_test.go (сравнение вывода с php -r).
func TestNamespaceVsPHP(t *testing.T) {
	cases := []string{
		// 1. обычный namespace + функция/константа/класс
		`<?php namespace Foo\Bar; function f(){ return "fn"; } class A { public $v = 7; } echo f(); $a = new A(); echo $a->v;`,
		// 2. __NAMESPACE__
		`<?php namespace Foo\Bar; echo __NAMESPACE__;`,
		// 3. __NAMESPACE__ в глобальном пространстве
		`<?php echo "[" . __NAMESPACE__ . "]";`,
		// 4. вызов \strlen (абсолютное имя)
		`<?php namespace Foo; echo \strlen("hello");`,
		// 5. неквалифицированный вызов глобальной функции из namespace (fallback)
		`<?php namespace Foo; echo strlen("abcd");`,
		// 6. наследование внутри namespace
		`<?php namespace N; class Base { public function v(){ return "base"; } } class Child extends Base {} $c = new Child(); echo $c->v();`,
		// 7. instanceof с коротким и полным именем
		`<?php namespace N; class A {} $o = new A(); var_dump($o instanceof A); var_dump($o instanceof \N\A);`,
		// 8. get_class внутри namespace
		`<?php namespace N; class A {} $o = new A(); echo get_class($o);`,
		// 9. два namespace (block form)
		`<?php namespace A { function fa(){ return "A"; } echo fa(); } namespace B { function fb(){ return "B"; } echo fb(); }`,
		// 10. use с alias класса (импорт из другого пространства)
		`<?php namespace Lib; class Service { public function hi(){ return "hi"; } } namespace App; use Lib\Service as S; $s = new S(); echo $s->hi();`,
		// 11. константа с fallback на глобальную
		`<?php namespace N; echo PHP_EOL === "\n" ? "eol" : "no";`,
		// 12. ведущий \ для класса при new/instanceof
		`<?php namespace N; class A {} $o = new \N\A(); echo $o instanceof \N\A ? "y" : "n";`,
		// 13. статический вызов и константа класса в namespace
		`<?php namespace N; class A { const K = 3; public static function m(){ return 9; } } echo A::K; echo A::m();`,
		// 14. use function + групповой use
		`<?php namespace Lib; function fmt($x){ return "[" . $x . "]"; } namespace App; use function Lib\fmt; echo fmt(5);`,
		// 15. get_class с алиасом класса
		`<?php namespace Lib; class Base {} namespace App; use Lib\Base as B; class Child extends B {} $c = new Child(); echo get_class($c);`,
		// 16. FQ-вызов собственной функции
		`<?php namespace N; function f(){ return 1; } echo \N\f();`,
		// 17. групповой use с alias
		`<?php namespace Lib; class Str { const X = 7; } namespace App; use Lib\{Str as S}; echo S::X;`,
		// 18. namespace сохраняется в теле функции
		`<?php namespace N; function g(){ return __NAMESPACE__; } echo g();`,
	}
	for _, src := range cases {
		runVsPHP(t, src)
	}
}

// TestNamespaceCompiles — парсер не падает на конструкциях use/namespace.
func TestNamespaceCompiles(t *testing.T) {
	srcs := []string{
		`<?php namespace Foo\Bar; echo 1;`,
		`<?php namespace Foo { echo 1; }`,
		`<?php use Foo\Bar; echo 1;`,
		`<?php use Foo\Bar as Baz; echo 1;`,
		`<?php use function Foo\bar; echo 1;`,
		`<?php use const Foo\BAR; echo 1;`,
		`<?php use Foo\{A, B as C}; echo 1;`,
		`<?php use function Foo\{a, b as c}; echo 1;`,
	}
	for _, src := range srcs {
		if _, err := Compile(src); err != nil {
			t.Errorf("Compile(%q) error: %v", src, err)
		}
	}
}

// TestNamespaceRuntime — ключевая семантика без живого php.
func TestNamespaceRuntime(t *testing.T) {
	run := func(src string) string {
		out, err := runCompiled(t, src)
		if err != nil {
			t.Fatalf("run %q: %v", src, err)
		}
		return out
	}
	if got := run(`<?php namespace N; class A {} $o = new A(); echo get_class($o);`); got != "N\\A" {
		t.Errorf("get_class = %q", got)
	}
	if got := run(`<?php namespace N; echo __NAMESPACE__;`); got != "N" {
		t.Errorf("__NAMESPACE__ = %q", got)
	}
	if got := run(`<?php namespace N; echo \strlen("1234");`); got != "4" {
		t.Errorf("\\strlen = %q", got)
	}
	if got := run(`<?php namespace N; echo strlen("12345");`); got != "5" {
		t.Errorf("strlen fallback = %q", got)
	}
	if got := run(`<?php namespace N; function f(){ return "ok"; } echo f();`); got != "ok" {
		t.Errorf("ns function = %q", got)
	}
}

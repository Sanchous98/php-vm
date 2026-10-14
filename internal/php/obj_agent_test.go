package php

import (
	"strings"
	"testing"
)

// TestObjFeatures — агентские регрессионные тесты по объектной модели:// enum, типы возврата/параметров, trait-адаптации, declare-time фаталы.
// Не требуют внешнего php: сравнивают вывод VM с ожидаемым.
func TestObjFeatures(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "enum-cases",
			src: `<?php
enum Suit { case Hearts; case Diamonds; }
echo count(Suit::cases()), "\n";
echo Suit::Hearts->name, "\n";
echo Suit::Hearts === Suit::cases()[0] ? "same" : "diff", "\n";
`,
			want: "2\nHearts\nsame\n",
		},
		{
			name: "enum-backed",
			src: `<?php
enum Suit: string { case Hearts = 'H'; case Diamonds = 'D'; }
echo Suit::from('H')->name, "\n";
var_dump(Suit::tryFrom('Z'));
`,
			want: "Hearts\nNULL\n",
		},
		{
			name: "enum-var-dump",
			src: `<?php
enum Suit { case Hearts; }
var_dump(Suit::Hearts);
`,
			want: "enum(Suit::Hearts)\n",
		},
		{
			name: "return-type-error",
			src: `<?php
function f(): array { return 1; }
f();
`,
			want: "Fatal error: Uncaught TypeError: f(): Return value must be of type array, int returned in php:2\nStack trace:\n#0 php(3): f()\n#1 {main}\n  thrown in php on line 2\n",
		},
		{
			name: "trait-insteadof",
			src: `<?php
trait A { public function m() { echo "A"; } }
trait B { public function m() { echo "B"; } }
class C { use A, B { A::m insteadof B; } }
(new C)->m();
`,
			want: "A",
		},
		{
			name: "trait-alias",
			src: `<?php
trait A { public function m() { echo "A"; } }
class C { use A { m as n; } }
$c = new C;
$c->m(); $c->n();
`,
			want: "AA",
		},
		{
			name: "abstract-method-fatal",
			src: `<?php
class fail { abstract function show(); }
`,
			want: "Fatal error: Class fail contains 1 abstract method and must therefore be declared abstract or implement the remaining methods (fail::show) in php on line 2\n",
		},
		{
			name: "private-method-message",
			src: `<?php
class P { private function s() {} }
(new P)->s();
`,
			want: "Fatal error: Uncaught Error: Call to private method P::s() from global scope in php:3\nStack trace:\n#0 {main}\n  thrown in php on line 3\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := runFile(c.src)
			if strings.TrimSpace(got) != strings.TrimSpace(c.want) {
				t.Errorf("%s:\n got=%q\nwant=%q", c.name, got, c.want)
			}
		})
	}
}

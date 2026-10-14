package php

import "testing"

// Регрессия SSA-аллокатора регистров: by-ref параметр, увеличиваемый в
// while-цикле, условие которого — присваивание результата вызова.
//
// Блок входа функции (block 0) имеет неявного предшественника — точку входа.
// Если цикл начинается первым оператором, block 0 одновременно является
// заголовком цикла, и в него ведёт обратное ребро. Старый разбор SSA строил
// phi только из реальных предшественников и тривиально сводил его к значению
// с обратного ребра, теряя входное значение pinned-регистра (параметра/
// by-ref ячейки). В результате ячейка ссылки «переезжала» в другой регистр,
// запись `$consumed += ...` уходила в мусор, а чтение отдавало ресурс-объект
// ("Unsupported operand types: resource + int" в streams-тестах).
//
// Баг проявлялся, только когда by-ref параметр НЕ первый: при одном параметре
// phi с одиночным операндом случайно получал верный цвет.
func TestSSAByRefParamLoopEntry(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			// by-ref второй параметр, условие цикла — вызов.
			name: "byref-second-param",
			src: `<?php
function mk() { static $i = 0; $i++; if ($i < 4) return 1; return 0; }
function f($a, &$c) { while ($b = mk()) { $c += 5; } }
$c = 0; f(1, $c); var_dump($c);
`,
			want: "int(15)\n",
		},
		{
			// сигнатура как у php_user_filter::filter: 4 параметра, by-ref третий.
			name: "four-params",
			src: `<?php
function mk() { static $i = 0; $i++; if ($i < 4) return 1; return 0; }
function f($a, $b2, &$c, $d): int { while ($b = mk()) { $c += 5; } return 0; }
$c = 0; f(1, 2, $c, 3); var_dump($c);
`,
			want: "int(15)\n",
		},
		{
			// метод с обёрткой-объектом (как в streams): поле bucket->datalen.
			name: "method-property-increment",
			src: `<?php
class B { public $data = 'x'; public $datalen = 5; }
function mk() { static $i = 0; $i++; if ($i < 4) return new B; return null; }
class F {
    public function filter($in, $out, &$consumed, $closing): int {
        while ($bucket = mk()) { $consumed += $bucket->datalen; }
        return 0;
    }
}
$c = 0; (new F)->filter(1, 2, $c, false); var_dump($c);
`,
			want: "int(15)\n",
		},
		{
			// loop-carried параметр, читаемый в заголовке: phi обязана нести
			// входное значение параметра на первой итерации.
			name: "loop-carried-param",
			src: `<?php
function f($x) { while ($x > 0) { $x = $x - 1; } return $x; }
var_dump(f(5));
`,
			want: "int(0)\n",
		},
		{
			name: "loop-carried-param-body-value",
			src: `<?php
function f($x) { while ($x > 0) { $y = $x; $x = $x - 1; } return $y; }
var_dump(f(5));
`,
			want: "int(1)\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runCompiled(t, tc.src)
			if err != nil {
				t.Fatalf("run error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

package php

import (
	"testing"
)

// Регрессионные тесты пересчёта позиций активных foreach by ref при
// перестройке массива (array_shift/unshift/splice) — поведение сверено с
// php 8.3 (Zend/tests/foreach_015/016/017, array_splice_empty_ht_iter_removal).
// Проверяются также: сброс курсора при смене объекта массива ($a = ...),
// удаление элемента под курсором (unset в теле), неизменность обхода чужого
// массива и вложенные обходы.

func TestForeachByRefIteratorRemap(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			// foreach_015: array_shift сохраняет позицию by-ref итератора.
			name: "shift",
			src: `<?php
$a = [1,2,3,4];
foreach($a as &$v) { echo "$v\n"; array_shift($a); }
var_dump($a);`,
			want: "1\n2\n3\n4\narray(0) {\n}\n",
		},
		{
			// foreach_016: array_unshift сдвигает курсор на число аргументов.
			name: "unshift",
			src: `<?php
$a = [1,2,3];
foreach($a as &$v) {
    echo "$v\n";
    if ($v == 2) { array_unshift($a, 0, 0, 0, 0, 0, 0, 0, 0); }
}
var_dump(count($a));`,
			want: "1\n2\n3\nint(11)\n",
		},
		{
			// foreach_017: удаление/замена до, после и на текущем элементе.
			name: "splice_before_after_current",
			src: `<?php
$done = 0; $a = [0,1,2,3,4];
foreach($a as &$v) { echo "$v\n"; if (!$done && $v == 3) { $done = 1; array_splice($a, 1, 2); } }
echo "\n";
$done = 0; $a = [0,1,2,3,4];
foreach($a as &$v) { echo "$v\n"; if (!$done && $v == 0) { $done = 1; array_splice($a, 2, 2); } }
echo "\n";
$done = 0; $a = [0,1,2,3,4];
foreach($a as &$v) { echo "$v\n"; if (!$done && $v == 2) { $done = 1; array_splice($a, 1, 3); } }
echo "\n";
$replacement = ['x','y','z'];
$done = 0; $a = [0,1,2,3,4];
foreach($a as &$v) { echo "$v\n"; if (!$done && $v == 3) { $done = 1; array_splice($a, 1, 2, $replacement); } }
echo "\n";
$done = 0; $a = [0,1,2,3,4];
foreach($a as &$v) { echo "$v\n"; if (!$done && $v == 0) { $done = 1; array_splice($a, 2, 2, $replacement); } }
echo "\n";
$done = 0; $a = [0,1,2,3,4];
foreach($a as &$v) { echo "$v\n"; if (!$done && $v == 2) { $done = 1; array_splice($a, 1, 3, $replacement); } }
echo "\n";`,
			want: "0\n1\n2\n3\n4\n\n0\n1\n4\n\n0\n1\n2\n4\n\n0\n1\n2\n3\n4\n\n0\n1\nx\ny\nz\n4\n\n0\n1\n2\n4\n\n",
		},
		{
			// array_splice_empty_ht_iter_removal: возврат splice, присвоенный
			// источнику, сбрасывает курсор на начало нового массива.
			name: "splice_assign_back_resets",
			src: `<?php
$a=[4]; $i = 0;
foreach ($a as &$r) {
    var_dump($r);
    $a = array_splice($a, 0);
    if (++$i == 2) break;
}`,
			want: "int(4)\nint(4)\n",
		},
		{
			// Удаление элемента под курсором (unset в теле) продвигает
			// курсор как zend_hash_packed_del_val, затем shift сдвигает.
			name: "unset_at_cursor_skips_tombstone",
			src: `<?php
$a=[1,2,3,4];
foreach($a as &$v){ echo "$v "; if($v==1){ unset($a[1]); } }
echo "\n";`,
			want: "1 3 4 \n",
		},
		{
			name: "unset_at_cursor_then_shift2",
			src: `<?php
$a=[1,2,3,4];
foreach($a as &$v){ echo "$v "; if($v==1){ unset($a[1]); array_shift($a); } }
echo "\n";`,
			want: "1 3 4 \n",
		},
		{
			// Переприсваивание источника в теле: Zend пересинхронизирует
			// итератор на начало нового массива.
			name: "reassign_source_resyncs",
			src: `<?php
$a=[1,2,3];
foreach($a as &$v){ echo "$v "; if($v==1){ $a=[7,8,9]; } }
echo "\n";`,
			want: "1 7 8 9 \n",
		},
		{
			// Перестройка чужого массива не должна двигать курсор этого.
			name: "other_array_untouched",
			src: `<?php
$b=[9,9]; $a=[1,2,3,4];
foreach($a as &$v){ echo "$v "; array_shift($b); }
echo "\n";`,
			want: "1 2 3 4 \n",
		},
		{
			// array_shift через функцию с by-ref параметром: ячейка та же.
			name: "shift_via_byref_function",
			src: `<?php
function sh(&$x){ array_shift($x); }
$a=[1,2,3,4];
foreach($a as &$v){ echo "$v "; sh($a); }
echo "\n";`,
			want: "1 2 3 4 \n",
		},
		{
			// Добавление в конец в теле цикла — итератор доходит до новых.
			name: "push_in_body_visible",
			src: `<?php
$a=[1,2]; $n=0;
foreach($a as &$v){ echo "$v "; if(++$n>8) break; array_push($a, 10+$v); }
echo "\n";`,
			want: "1 2 11 12 21 22 31 32 41 \n",
		},
		{
			// array_pop не сдвигает прочие позиции; после исчерпания — конец.
			name: "pop_in_body",
			src: `<?php
$a=[1,2,3,4];
foreach($a as &$v){ echo "$v "; if($v==2) array_pop($a); }
echo "\n";`,
			want: "1 2 3 \n",
		},
		{
			// Смешанные ключи: строковые сохраняются, числовые перенумеруются,
			// курсор продолжает с того же элемента.
			name: "mixed_keys_shift",
			src: `<?php
$a=[5=>'a','x'=>'b',9=>'c',10=>'d'];
foreach($a as $k=>&$v){ echo "$k=$v "; if($k===5) array_shift($a); }
echo "\n";`,
			want: "5=a x=b 0=c 1=d \n",
		},
		{
			// Замена с ростом массива: удалённый под курсором элемент
			// переводит курсор к первому уцелевшему после диапазона.
			name: "splice_deleted_grow",
			src: `<?php
$a=[1,2];
foreach($a as &$v){ echo "$v "; if($v==1) array_splice($a,1,1,['x','y','z']); }
echo "\n";`,
			want: "1 y z \n",
		},
		{
			// Вставка в конец при курсоре за концом: Zend продолжает обход
			// на вставленные элементы.
			name: "splice_at_end_continues",
			src: `<?php
$a=[1,2,3]; $n=0;
foreach($a as &$v){ echo "$v "; if($v==3){ array_splice($a,0,0,['x','y']); if(++$n>4) break; } }
echo "\n";`,
			want: "1 2 3 2 3 2 3 2 3 2 3 \n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runPHPCode(t, tc.src)
			if got != tc.want {
				t.Fatalf("вывод не совпал:\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

// TestForeachByRefIteratorRemapNested — два вложенных by-ref обхода одного
// массива имеют независимые курсоры; перестройка в теле внутреннего двигает
// оба корректно (сверено с php 8.3).
func TestForeachByRefIteratorRemapNested(t *testing.T) {
	got := runPHPCode(t, `<?php
$a = [1,2,3];
$n = 0;
foreach($a as &$v) {
    foreach($a as &$w) {
        echo "($v,$w)";
        if(++$n>20){ break 2; }
    }
    echo "\n";
}
echo "\n";
`)
	want := "(1,1)(1,2)(1,3)\n(2,1)(2,2)(2,3)\n(3,1)(3,2)(3,3)\n\n"
	if got != want {
		t.Fatalf("nested: got %q want %q", got, want)
	}
}

// TestForeachByRefIteratorRemapNoByRef — массивы, не участвующие в foreach by
// ref, не должны сканировать регистры: перестройки работают как раньше.
func TestForeachByRefIteratorRemapPlain(t *testing.T) {
	got := runPHPCode(t, `<?php
$a = [1,2,3,4,5];
foreach($a as $k => $v) { if ($v == 2) { array_shift($a); } echo "$k=$v "; }
echo "\n";
$b = [1,2,3];
$r = array_splice($b, 1, 1, [9]);
echo count($b), " ", count($r), "\n";
`)
	want := "0=1 1=2 2=3 3=4 4=5 \n3 1\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

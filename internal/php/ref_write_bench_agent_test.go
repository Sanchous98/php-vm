package php

import "testing"

// Бенчмарки записи через by-ref переменную: раньше каждый append/index-set
// помечал массив разделённым (RefGet/RefSet), а Unshare копировал его целиком
// — O(n^2) и десятки гигабайт аллокаций. Сравнение с php — /tmp/bench.
//
//	go test -vet=off -run '^$' -bench '^BenchmarkRef' -benchtime 1x -benchmem ./internal/php/

// 30k append к переменной, помеченной by-ref мёртвым sort().
func BenchmarkRefAppend30k(b *testing.B) {
	benchPHP(b, `<?php
$a = [];
if (false) { sort($a); }
for ($i = 0; $i < 30000; $i++) { $a[] = $i; }
echo count($a), "\n";
`)
}

// 30k array_push в цикле: массив дописывается на месте, без копий.
func BenchmarkRefArrayPush30k(b *testing.B) {
	benchPHP(b, `<?php
$a = [];
for ($i = 0; $i < 30000; $i++) { array_push($a, $i); }
echo count($a), "\n";
`)
}

// Запись по ключу через by-ref переменную (в т.ч. op=).
func BenchmarkRefKeyed30k(b *testing.B) {
	benchPHP(b, `<?php
$a = [];
if (false) { sort($a); }
for ($i = 0; $i < 30000; $i++) { $a[$i % 100] = $i; $a[0] += 1; }
echo count($a), "\n";
`)
}

// Вложенный append через by-ref переменную: контейнер и владелец не
// копируются.
func BenchmarkRefNestedAppend30k(b *testing.B) {
	benchPHP(b, `<?php
$a = [];
if (false) { sort($a); }
for ($i = 0; $i < 30000; $i++) { $a[0][] = $i; }
echo count($a[0]), "\n";
`)
}

// Заполнение + sort: после sort ячейка снова уникальный владелец, append
// остаются in-place.
func BenchmarkRefFillSort30k(b *testing.B) {
	benchPHP(b, `<?php
$a = [];
for ($i = 0; $i < 30000; $i++) { $a[] = ($i * 7919) % 1000000; }
sort($a);
echo $a[0], "\n";
`)
}

// Append через by-ref параметр функции.
func BenchmarkRefParamAppend30k(b *testing.B) {
	benchPHP(b, `<?php
function f(&$x) { for ($i = 0; $i < 30000; $i++) { $x[] = $i; } }
$a = []; f($a);
echo count($a), "\n";
`)
}

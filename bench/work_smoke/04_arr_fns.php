<?php
$n = 60000; $a = [];
for ($i = 0; $i < $n; $i++) { $a[] = $i % 1000; }
$b = array_map(function ($v) { return $v * 2; }, $a);
$c = array_filter($b, function ($v) { return $v % 3 === 0; });
$d = array_reduce($c, function ($acc, $v) { return $acc + $v; }, 0);
$e = array_merge(array_slice($b, 0, 1000), array_slice($a, 0, 1000));
$u = array_unique($a);
$r = array_reverse($u);
echo count($e), ' ', count($u), ' ', $d, "\n";

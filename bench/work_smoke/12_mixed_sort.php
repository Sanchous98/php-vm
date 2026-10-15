<?php
$n = 60000; $a = [];
for ($i = 0; $i < $n; $i++) { $a[] = 'item' . (($i * 7919) % 100000) . '-' . ($i % 97); }
usort($a, function ($x, $y) { return strcmp($x, $y); });
$m = [];
for ($i = 0; $i < 20000; $i++) { $m['k' . $i] = ($i * 2654435761) % 1000000; }
uasort($m, function ($x, $y) { return $x <=> $y; });
echo $a[0], ' ', count($m), "\n";

<?php
$n = 120000; $a = []; $x = 12345;
for ($i = 0; $i < $n; $i++) { $x = ($x * 1103515245 + 12345) & 0x7fffffff; $a[] = $x % 1000000; }
sort($a);
$c = 0; foreach ($a as $v) { if ($v > 500000) $c++; }
rsort($a);
echo $c, "\n";

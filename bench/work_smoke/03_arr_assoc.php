<?php
$n = 150000; $m = [];
for ($i = 0; $i < $n; $i++) { $m['k' . $i] = $i; }
$s = 0;
for ($i = 0; $i < $n; $i += 3) { $s += $m['k' . $i]; }
foreach ($m as $k => $v) { if ($v % 7 === 0) unset($m[$k]); }
echo $s, ' ', count($m), "\n";

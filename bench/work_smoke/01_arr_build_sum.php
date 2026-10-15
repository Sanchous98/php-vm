<?php
$n = 300000;
$a = [];
for ($i = 0; $i < $n; $i++) { $a[] = $i; }
$s = 0;
foreach ($a as $v) { $s += $v; }
echo $s, "\n";

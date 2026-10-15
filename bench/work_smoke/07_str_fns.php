<?php
$n = 60000; $acc = '';
for ($i = 0; $i < $n; $i++) {
    $acc = strtolower(trim(sprintf('  Item-%d-%s  ', $i, str_repeat('x', $i % 7))));
    if ($i % 1000 === 0) { $acc = implode(',', explode('-', $acc)); }
}
echo $acc, "\n";

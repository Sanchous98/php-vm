<?php
$s = 'x'; $t = '';
for ($i = 0; $i < 40000; $i++) {
    $t = substr($s, 0, 5) . 'y' . $i;
    $s = str_replace('x', 'z', $t);
    if (strpos($s, 'z') === false) { $s = 'x'; }
}
echo strlen($s), "\n";

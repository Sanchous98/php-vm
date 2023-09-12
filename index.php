<?php

function test($n) {
    $n[] = 1;
}

$x = [0];

$time = microtime(true);
for ($i = 0; $i < 100000; $i++) {
    test($x);
}

echo microtime(true) - $time;
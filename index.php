<?php

function fibonacci(int $n) {
    if ($n === 0) {
        return 0;
    }

    if ($n === 1) {
        return 1;
    }

    return fibonacci($n - 1) + fibonacci($n - 2);
}

$time = microtime(true);
$x = 35;
fibonacci($x);
echo microtime(true) - $time;

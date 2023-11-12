<?php

$x = 0;
$time = microtime(true);

for ($i = 0; $i < 100000000; $i++) {
    $x++;
}

echo microtime(true) - $time;
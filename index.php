<?php

function test(int $i): int {
    echo $i, "\n";
}

for ($i = 0; $i < 1000; $i++) {
    test($i);
}
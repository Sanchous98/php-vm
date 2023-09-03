<?php

const ticks = 1;

function fibonacci($n)
{
	if ($n === 0) {
		return 0;
	}

	if ($n < 2) {
		return 1;
	}

	$prev = 1;
	$current = 1;

	for ($i = 2; $i < $n; $i++) {
		$temp = $prev + $current;
		$prev = $current;
		$current = $temp;
	}

	return $current;
}


$time = microtime(true);

for ($i = 0; $i < 100000; $i++) {
    fibonacci(1000);
}

echo microtime(true) - $time, "\n";

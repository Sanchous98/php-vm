<?php

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
	$all = [1,1];

	for ($i = 2; $i < $n; $i++) {
		$temp = $prev + $current;
		$prev = $current;
		$current = $temp;
		$all[] = $current;
	}

	return $all;
}

$time = microtime(true);
$n = fibonacci(10);
$n[] = ["test"];
var_dump($n);
echo microtime(true) - $time;
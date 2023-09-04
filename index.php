<?php

function fibonacci($n, &$ret)
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

	$ret = $current;
}

fibonacci(10, $ret);

echo $ret;

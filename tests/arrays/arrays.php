<?php

$arr = [1, "test" => 2];
$arr[] = 3;
$arr['test2'] = 4;
$read = $arr['test'];
return $arr;
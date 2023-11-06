<?php

for ($i = 0; $i < 100000; $i++) {
    $arr = [1, "test" => 2];
    $arr[] = 3;
    $arr['test4'][] = 4;
    $arr['test4']['test2'] = 5;
    $arr["test3"] = 6;
    $read = $arr["test"];
}

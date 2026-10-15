<?php
function build($d, $b) {
    if ($d === 0) { return mt_rand(0, 100); }
    $a = [];
    for ($i = 0; $i < $b; $i++) { $a['k' . $i] = build($d - 1, $b); }
    return $a;
}
mt_srand(1234);
$total = 0;
for ($i = 0; $i < 60; $i++) {
    $tree = build(4, 4);
    $js = json_encode($tree);
    $back = json_decode($js, true);
    $total += strlen($js) + count($back);
}
echo $total, "\n";

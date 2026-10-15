<?php
class P { public $a = 0; public $b = 0; public $c = 0; public $d = 0; public $e = 0; }
$p = new P;
for ($i = 0; $i < 700000; $i++) {
    $p->a = $i; $p->b = $p->a + 1; $p->c = $p->b + 1; $p->d = $p->c + 1; $p->e = $p->d + 1;
}
echo $p->e, "\n";

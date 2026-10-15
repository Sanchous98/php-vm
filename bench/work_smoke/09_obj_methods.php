<?php
class M { private $v = 0; public function add($x) { $this->v += $x; return $this; } public function get() { return $this->v; } }
$m = new M;
for ($i = 0; $i < 800000; $i++) { $m->add($i % 10); }
echo $m->get(), "\n";

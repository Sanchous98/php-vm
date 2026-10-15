<?php
$s = '';
for ($i = 0; $i < 300000; $i++) { $s .= 'abc'; }
echo strlen($s), "\n";

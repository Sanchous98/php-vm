<?php
// Reference matrix: conversions and truthiness, one line per case.
// Regenerate with: php testdata/casts.php > testdata/casts.tsv
$vals = [
  'null'    => null,
  'false'   => false,
  'true'    => true,
  'int0'    => 0,
  'int1'    => 1,
  'intm1'   => -1,
  'int42'   => 42,
  'intmax'  => PHP_INT_MAX,
  'intmin'  => PHP_INT_MIN,
  'float0'  => 0.0,
  'float15' => 1.5,
  'floatm0' => -0.0,
  'floate'  => 1.0e20,
  'floatf'  => 0.1 + 0.2,
  'emptystr'=> "",
  'str0'    => "0",
  'str00'   => "00",
  'strnum'  => "123",
  'strlead' => "  12",
  'strtail' => "12abc",
  'strhex'  => "0x1A",
  'strex'   => "1e2",
  'strbig'  => "1e20",
  'strbig2' => "9999999999999999999",
  'strneg'  => "-7",
  'strf'    => "1.5",
  'strfdot' => ".5",
  'stra'    => "abc",
  'strsp'   => " ",
];

foreach ($vals as $name => $v) {
  echo "type\t$name\t" . gettype($v) . "\n";
  echo "bool\t$name\t" . var_export((bool)$v, true) . "\n";
  echo "int\t$name\t"  . var_export((int)$v, true) . "\n";
  echo "float\t$name\t" . var_export((float)$v, true) . "\n";
  echo "str\t$name\t"  . bin2hex((string)$v) . "\n";   // hex to survive NUL/whitespace
}

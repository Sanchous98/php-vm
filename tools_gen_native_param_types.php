<?php
$funcs = get_defined_functions()['internal'];
$out = [];
foreach ($funcs as $name) {
    $r = new ReflectionFunction($name);
    if ($r->isDisabled()) continue;
    $types = [];
    $any = false;
    foreach ($r->getParameters() as $p) {
        $t = $p->getType();
        if ($t === null) { $types[] = ''; continue; }
        $s = (string)$t;
        if ($p->isVariadic()) $s = '...' . $s;
        $types[] = $s;
        $any = true;
    }
    if ($any) $out[strtolower($name)] = $types;
}
ksort($out);
$w = 0;
foreach ($out as $k => $v) { $w = max($w, strlen($k)); }
$esc = fn($s) => '"' . str_replace(['\\', '"'], ['\\\\', '\\"'], $s) . '"';
echo "package php\n\n// native_param_types.go — типы параметров internal-функций PHP 8.3.\n// Сгенерировано из ReflectionFunction живого php:\n//\n//\tphp tools/gen_native_param_types.php > internal/php/native_param_types.go\n//\n// Пустая строка — параметр без объявленного типа; префикс \"...\" — вариативный.\nvar nativeParamTypes = map[string][]string{\n";
foreach ($out as $k => $v) {
    $q = array_map($esc, $v);
    printf("\t%-{$w}s {%s},\n", '"' . $k . '"', implode(', ', $q));
}
echo "}\n";
fwrite(STDERR, "count=" . count($out) . "\n");

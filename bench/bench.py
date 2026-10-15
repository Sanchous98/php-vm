#!/usr/bin/env python3
"""Комплексные нагрузки: наш VM (тест-бинарь, ns/op) vs php (стенное время+RSS).

Usage: bench.py <testbinary> <workdir> [N]
Генерирует 12 нагрузок *.php в workdir, гоняет N раз каждую (чередуя порядок
наш/php), печатает min время и max RSS.
"""
import os, sys, time, re, tempfile, shutil, shlex

TESTBIN = sys.argv[1] if len(sys.argv) > 1 else '/home/dev/wavebench/bench/phpvm.test'
# PHP для сравнения: PHP_BIN из окружения, иначе php из PATH, иначе /usr/bin/php.
# PHP_ARGS — дополнительные опции php (например, JIT), разбираются как shell-слова.
PHP_BIN = os.environ.get('PHP_BIN') or shutil.which('php') or '/usr/bin/php'
PHP_ARGS = shlex.split(os.environ.get('PHP_ARGS', ''))
WORK = sys.argv[2] if len(sys.argv) > 2 else '/home/dev/wavebench/bench/work'
N = int(sys.argv[3]) if len(sys.argv) > 3 else 3

LOADS = {
'01_arr_build_sum': '''<?php
$n = 300000;
$a = [];
for ($i = 0; $i < $n; $i++) { $a[] = $i; }
$s = 0;
foreach ($a as $v) { $s += $v; }
echo $s, "\\n";
''',
'02_arr_sort': '''<?php
$n = 120000; $a = []; $x = 12345;
for ($i = 0; $i < $n; $i++) { $x = ($x * 1103515245 + 12345) & 0x7fffffff; $a[] = $x % 1000000; }
sort($a);
$c = 0; foreach ($a as $v) { if ($v > 500000) $c++; }
rsort($a);
echo $c, "\\n";
''',
'03_arr_assoc': '''<?php
$n = 150000; $m = [];
for ($i = 0; $i < $n; $i++) { $m['k' . $i] = $i; }
$s = 0;
for ($i = 0; $i < $n; $i += 3) { $s += $m['k' . $i]; }
foreach ($m as $k => $v) { if ($v % 7 === 0) unset($m[$k]); }
echo $s, ' ', count($m), "\\n";
''',
'04_arr_fns': '''<?php
$n = 60000; $a = [];
for ($i = 0; $i < $n; $i++) { $a[] = $i % 1000; }
$b = array_map(function ($v) { return $v * 2; }, $a);
$c = array_filter($b, function ($v) { return $v % 3 === 0; });
$d = array_reduce($c, function ($acc, $v) { return $acc + $v; }, 0);
$e = array_merge(array_slice($b, 0, 1000), array_slice($a, 0, 1000));
$u = array_unique($a);
$r = array_reverse($u);
echo count($e), ' ', count($u), ' ', $d, "\\n";
''',
'05_str_chain': '''<?php
$s = 'x'; $t = '';
for ($i = 0; $i < 40000; $i++) {
    $t = substr($s, 0, 5) . 'y' . $i;
    $s = str_replace('x', 'z', $t);
    if (strpos($s, 'z') === false) { $s = 'x'; }
}
echo strlen($s), "\\n";
''',
'06_str_builder': '''<?php
$s = '';
for ($i = 0; $i < 300000; $i++) { $s .= 'abc'; }
echo strlen($s), "\\n";
''',
'07_str_fns': '''<?php
$n = 60000; $acc = '';
for ($i = 0; $i < $n; $i++) {
    $acc = strtolower(trim(sprintf('  Item-%d-%s  ', $i, str_repeat('x', $i % 7))));
    if ($i % 1000 === 0) { $acc = implode(',', explode('-', $acc)); }
}
echo $acc, "\\n";
''',
'08_obj_props': '''<?php
class P { public $a = 0; public $b = 0; public $c = 0; public $d = 0; public $e = 0; }
$p = new P;
for ($i = 0; $i < 700000; $i++) {
    $p->a = $i; $p->b = $p->a + 1; $p->c = $p->b + 1; $p->d = $p->c + 1; $p->e = $p->d + 1;
}
echo $p->e, "\\n";
''',
'09_obj_methods': '''<?php
class M { private $v = 0; public function add($x) { $this->v += $x; return $this; } public function get() { return $this->v; } }
$m = new M;
for ($i = 0; $i < 800000; $i++) { $m->add($i % 10); }
echo $m->get(), "\\n";
''',
'10_recursion': '''<?php
function fib($n) { if ($n < 2) { return $n; } return fib($n - 1) + fib($n - 2); }
echo fib(28), "\\n";
''',
'11_mixed_json': '''<?php
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
echo $total, "\\n";
''',
'12_mixed_sort': '''<?php
$n = 60000; $a = [];
for ($i = 0; $i < $n; $i++) { $a[] = 'item' . (($i * 7919) % 100000) . '-' . ($i % 97); }
usort($a, function ($x, $y) { return strcmp($x, $y); });
$m = [];
for ($i = 0; $i < 20000; $i++) { $m['k' . $i] = ($i * 2654435761) % 1000000; }
uasort($m, function ($x, $y) { return $x <=> $y; });
echo $a[0], ' ', count($m), "\\n";
''',
}


def gen():
    os.makedirs(WORK, exist_ok=True)
    for name, src in LOADS.items():
        with open(os.path.join(WORK, name + '.php'), 'w') as f:
            f.write(src)


def run_once(argv, env_extra=None, want_out=False):
    out_path = None
    out_fd = None
    if want_out:
        out_fd, out_path = tempfile.mkstemp(dir=WORK)
    t0 = time.perf_counter()
    pid = os.fork()
    if pid == 0:
        try:
            devnull = os.open('/dev/null', os.O_WRONLY)
            os.dup2(devnull, 2)
            if out_fd is not None:
                os.dup2(out_fd, 1)
            if env_extra:
                for k, v in env_extra.items():
                    os.environ[k] = v
            os.execv(argv[0], argv)
        except Exception:
            pass
        os._exit(127)
    _, status, ru = os.wait4(pid, 0)
    dt = (time.perf_counter() - t0) * 1000.0
    cpu = (ru.ru_utime + ru.ru_stime) * 1000.0
    out = ''
    if out_fd is not None:
        os.close(out_fd)
        out = open(out_path).read()
        os.unlink(out_path)
    return dt, cpu, ru.ru_maxrss / 1024.0, out


def ours_cpu_ms(name, extra=5):
    """CPU одной итерации нагрузки (мс) = (cpu((1+extra)x) - cpu(1x)) / extra.
    Устойчиво к посторонней загрузке машины (в отличие от wall-clock ns/op)."""
    c1 = c2 = None
    rss = 0.0
    for bt, reps in (('1x', 0), (f'{1+extra}x', extra)):
        best = None
        for _ in range(2):
            _, cpu, r, _ = run_once(
                [TESTBIN, '-test.run', '^$', '-test.bench',
                 f'^BenchmarkComplex/{name}$', '-test.benchtime', bt],
                {'CX_DIR': WORK}, want_out=True)
            best = cpu if best is None else min(best, cpu)
            rss = max(rss, r)
        if reps == 0:
            c1 = best
        else:
            c2 = best
    per = (c2 - c1) / extra if (c2 or 0) > (c1 or 0) else 0.0
    return per, rss


def main():
    if not os.path.exists(TESTBIN):
        print('нет тест-бинарника:', TESTBIN)
        sys.exit(1)
    gen()
    print(f"{'нагрузка':22} {'наш_CPU':>8} {'php_CPU':>8} {'ratio':>6} {'наш_RSS':>8} {'php_RSS':>8}")
    for name in sorted(LOADS):
        ours, our_rss = ours_cpu_ms(name)
        php_cpu = None
        php_rss = 0.0
        for _ in range(N):
            _, cpu, rss, _ = run_once([PHP_BIN, *PHP_ARGS, os.path.join(WORK, name + '.php')])
            php_cpu = cpu if php_cpu is None else min(php_cpu, cpu)
            php_rss = max(php_rss, rss)
        ratio = ours / php_cpu if php_cpu else 0.0
        print(f"{name:22} {ours:8.1f} {php_cpu:8.1f} {ratio:6.2f} {our_rss:8.1f} {php_rss:8.1f}", flush=True)


if __name__ == '__main__':
    main()

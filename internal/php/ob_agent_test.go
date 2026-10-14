package php

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// obDiagRe убирает файл:строку из диагностических сообщений, чтобы сравнивать
// вывод VM с живым php (у них разные имена файла и номера строк).
var obDiagRe = regexp.MustCompile(` in [^\r\n]* on line \d+`)

func obNormalize(s string) string {
	return obDiagRe.ReplaceAllString(s, " in X on line N")
}

// TestOutputBufferingVsPHP — дифференциальные кейсы ob_* против живого php 8.3.
func TestOutputBufferingVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// базовые: буфер + echo + get_clean
		`<?php ob_start(); echo "x"; $s = ob_get_clean(); echo $s;`,
		`<?php ob_start(); echo "a"; ob_end_flush();`,
		`<?php ob_start(); echo "foo";`,
		`<?php echo "0"; ob_start(); echo "1"; ob_end_flush(); echo "2";`,

		// вложенность
		`<?php ob_start(); echo "a"; ob_start(); echo "b"; ob_end_clean(); echo "c"; ob_end_flush();`,
		`<?php ob_start(); echo "a"; ob_start(); echo "b"; $x = ob_get_clean(); ob_end_flush(); echo $x;`,
		`<?php ob_start(); ob_start(); echo "deep"; ob_end_flush(); echo "-"; ob_end_flush();`,

		// get_contents / get_level / length
		`<?php ob_start(); echo "foo"; echo ob_get_contents();`,
		`<?php var_dump(ob_get_level()); ob_start(); var_dump(ob_get_level()); ob_end_clean(); var_dump(ob_get_level());`,
		`<?php ob_start(); echo "hello"; var_dump(ob_get_length()); ob_end_clean();`,
		`<?php var_dump(ob_get_length());`,

		// clean / flush / get_flush
		`<?php ob_start(); echo "foo"; ob_clean(); echo "bar"; ob_end_flush();`,
		`<?php ob_start(); echo "foo"; ob_flush(); echo "bar"; ob_flush();`,
		`<?php ob_start(); echo "foo"; var_dump(ob_get_flush());`,
		`<?php ob_start(); echo "foo"; var_dump(ob_get_clean());`,

		// колбэки
		`<?php ob_start("strtoupper"); echo "hello"; ob_end_flush();`,
		`<?php function cb($s, $phase){ return "[$phase]$s"; } ob_start("cb"); echo "abc"; ob_end_flush();`,
		`<?php $f = function($s){ return strrev($s); }; ob_start($f); echo "abc"; ob_end_flush();`,
		`<?php class C { static function out($s){ return strtoupper($s); } } ob_start(["C", "out"]); echo "abc"; ob_end_flush();`,
		`<?php ob_start("strtoupper"); echo "ab"; echo "cd";`,

		// chunk_size
		`<?php ob_start("strtoupper", 3); echo "ab"; echo "cd"; ob_end_flush();`,
		`<?php ob_start("strtoupper", 0); echo "abcdef"; ob_end_flush();`,

		// list handlers / status
		`<?php print_r(ob_list_handlers());`,
		`<?php ob_start(); ob_start("strtoupper"); print_r(ob_list_handlers()); ob_end_clean(); ob_end_clean();`,
		`<?php ob_start(); $s = ob_get_status(true); ob_end_clean(); var_dump($s);`,
		`<?php var_dump(ob_get_status(true));`,

		// implicit flush
		`<?php var_dump(ob_implicit_flush()); var_dump(ob_implicit_flush(true));`,
		`<?php ob_start(); ob_implicit_flush(1); echo "hidden"; ob_end_clean(); echo "done";`,

		// без буфера
		`<?php var_dump(ob_end_clean());`,
		`<?php var_dump(ob_end_flush());`,
		`<?php var_dump(ob_flush());`,
		`<?php var_dump(ob_clean());`,
		`<?php var_dump(ob_get_clean());`,
		`<?php var_dump(ob_get_contents());`,
		`<?php var_dump(ob_get_flush());`,
	}
	for _, src := range cases {
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-d", "display_errors=1", "-d", "error_reporting=E_ALL", "-r", code).Output()
		if werr != nil {
			continue
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q:\n  run error: %v", src, err)
			continue
		}
		if obNormalize(got) != obNormalize(string(want)) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}

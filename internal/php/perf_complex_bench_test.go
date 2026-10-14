package php

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Комплексные end-to-end нагрузки из каталога (по умолчанию /tmp/bench/work).
// Те же .php-файлы гоняются в php — сравнение см. /tmp/bench/run_complex.sh.

func cxDir() string {
	if d := os.Getenv("CX_DIR"); d != "" {
		return d
	}
	return "/tmp/bench/work"
}

func BenchmarkComplex(b *testing.B) {
	files, _ := filepath.Glob(filepath.Join(cxDir(), "*.php"))
	if len(files) == 0 {
		b.Skipf("нет нагрузок в %s", cxDir())
	}
	sort.Strings(files)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(f), ".php")
		b.Run(name, func(b *testing.B) {
			benchPHP(b, string(src))
		})
	}
}

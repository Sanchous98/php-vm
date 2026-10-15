package php

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// z2c_dump_agent_test.go — агент «z2_other_b» (вторая половина корневых
// Zend/tests/*.phpt, не bug*/gh*/oss*). Вспомогательный инструмент:
// выкладывает полный got/want (и вывод живого php при Z2C_PHP=1) по каждому
// тесту из PHPT_LIST в каталог Z2C_OUT, чтобы диффить обычным diff.
//
//	PHPT_DIR=/home/dev/z2c_corp PHPT_LIST=... Z2C_OUT=/home/dev/z2c_logs/dump \
//	Z2C_PHP=1 go test -run TestZ2CDump -v ./internal/php/
func TestZ2CDump(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	lp := os.Getenv("PHPT_LIST")
	outDir := os.Getenv("Z2C_OUT")
	if root == "" || lp == "" || outDir == "" {
		t.Skip("PHPT_DIR/PHPT_LIST/Z2C_OUT не заданы")
	}
	data, err := os.ReadFile(lp)
	if err != nil {
		t.Fatal(err)
	}
	var pats []string
	for _, ln := range strings.Split(string(data), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			pats = append(pats, ln)
		}
	}
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
			for _, pat := range pats {
				if strings.Contains(p, pat) {
					files = append(files, p)
					break
				}
			}
		}
		return nil
	})
	sort.Strings(files)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	pass, fail, skip := 0, 0, 0
	for _, f := range files {
		data, _ := os.ReadFile(f)
		p := parsePhpt(string(data))
		if p.HasSkipIf || p.HasExpectRe || p.HasFileExt || p.File == "" || (!p.HasExpect && !p.HasExpectf) {
			skip++
			continue
		}
		if hasUnsupportedExt(p.Extensions) {
			skip++
			continue
		}
		if only := os.Getenv("PHPT_ONLY"); only != "" {
			good := false
			for _, s := range strings.Split(only, ",") {
				if s != "" && strings.Contains(f, s) {
					good = true
					break
				}
			}
			if !good {
				continue
			}
		}
		if excl := os.Getenv("PHPT_EXCLUDE"); excl != "" {
			bad := false
			for _, s := range strings.Split(excl, ",") {
				if s != "" && strings.Contains(f, s) {
					bad = true
					break
				}
			}
			if bad {
				continue
			}
		}
		base := strings.TrimSuffix(filepath.Base(f), ".phpt")
		if strings.Contains(f, "/Zend/tests/") {
			base = "Zend_" + base
		}
		want := p.Expect
		if p.HasExpectf {
			want = p.Expectf
		}
		os.WriteFile(filepath.Join(outDir, base+".want"), []byte(want+"\n"), 0644)
		os.WriteFile(filepath.Join(outDir, base+".flag"), []byte(fmt.Sprintf("expectf=%v\nsrc=%s\n", p.HasExpectf, f)), 0644)
		got, rerr := runPhptAt(p, f)
		os.WriteFile(filepath.Join(outDir, base+".got"), []byte(got), 0644)
		os.WriteFile(filepath.Join(outDir, base+".err"), []byte(fmt.Sprintf("%v\n", rerr)), 0644)
		ok := rerr == nil
		if throwableOf(rerr) != nil {
			ok = true
		}
		g := stripStartup(phpTrim(strings.ReplaceAll(got, "\r\n", "\n")))
		w := stripStartup(phpTrim(want))
		if ok {
			if p.HasExpect {
				ok = g == w
			}
			if ok && p.HasExpectf {
				ok = expectfMatch(w, g)
			}
		}
		status := "PASS"
		if ok {
			pass++
		} else {
			fail++
			status = "FAIL"
		}
		os.WriteFile(filepath.Join(outDir, base+".status"), []byte(status+"\n"), 0644)
		if os.Getenv("Z2C_PHP") == "1" {
			if real, perr := runRealPHP(p); perr == nil {
				os.WriteFile(filepath.Join(outDir, base+".php"), []byte(real), 0644)
			} else {
				os.WriteFile(filepath.Join(outDir, base+".php"), []byte(fmt.Sprintf("REALPHP ERR: %v\n", perr)), 0644)
			}
		}
	}
	t.Logf("dump pass=%d fail=%d skip=%d out=%s", pass, fail, skip, outDir)
}

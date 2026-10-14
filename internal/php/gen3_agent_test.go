package php

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestGen3Files прогоняет конкретные .phpt (список в GEN3_FILES через ';'),
// чтобы быстро сверять целевые кейсы без обхода всего каталога.
//
//	GEN3_FILES=/path/a.phpt;/path/b.phpt go test -run TestGen3Files -v ./internal/php/
func TestGen3Files(t *testing.T) {
	list := os.Getenv("GEN3_FILES")
	if list == "" {
		t.Skip("GEN3_FILES не задан")
	}
	files := strings.Split(list, ";")
	sort.Strings(files)
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		p := parsePhpt(string(data))
		got, rerr := runPhptAt(p, f)
		if throwableOf(rerr) != nil {
			rerr = nil
		}
		ok := rerr == nil
		if ok && p.HasExpect {
			ok = stripStartup(phpTrim(got)) == stripStartup(phpTrim(p.Expect))
		}
		if ok && p.HasExpectf {
			ok = expectfMatch(stripStartup(phpTrim(p.Expectf)), stripStartup(phpTrim(got)))
		}
		if !ok {
			t.Errorf("FAIL %s\n got=%q\n want=%q\n err=%v", filepath.Base(f), got, p.Expect+p.Expectf, rerr)
		} else {
			t.Logf("PASS %s", filepath.Base(f))
		}
	}
}

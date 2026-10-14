package php

import (
	"os"
	"testing"
)

// TestArray2Diff — дифференциальный прогон одного .phpt.
//
//	DIFF_FILE=/path/to/test.phpt go test -run TestArray2Diff -v ./internal/php/
//
// Печатает got и EXPECT/EXPECTF, а также результат сопоставления.
func TestArray2Diff(t *testing.T) {
	f := os.Getenv("DIFF_FILE")
	if f == "" {
		t.Skip("DIFF_FILE не задан")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	got, rerr := runPhptAt(p, f)
	t.Logf("--- GOT ---\n%s", got)
	if p.HasExpect {
		t.Logf("--- EXPECT ---\n%s", p.Expect)
		t.Logf("expect equal: %v", phpTrim(got) == phpTrim(p.Expect))
	}
	if p.HasExpectf {
		t.Logf("--- EXPECTF ---\n%s", p.Expectf)
		t.Logf("expectf match: %v", expectfMatch(phpTrim(p.Expectf), phpTrim(got)))
	}
	if rerr != nil {
		t.Logf("run err: %v", rerr)
	}
}

package php

import (
	"os"
	"testing"
)

// TestGFExpectfProbe проверяет EXPECTF-матчинг на сохранённых got/want
// (агентская отладка: GF_GOT и GF_WANT — пути к файлам).
func TestGFExpectfProbe(t *testing.T) {
	gw := os.Getenv("GF_GOT")
	ww := os.Getenv("GF_WANT")
	if gw == "" || ww == "" {
		t.Skip("GF_GOT/GF_WANT")
	}
	got, _ := os.ReadFile(gw)
	want, _ := os.ReadFile(ww)
	if !expectfMatch(string(want), string(got)) {
		t.Errorf("no match\nwant=%q\ngot=%q", string(want), string(got))
	}
}

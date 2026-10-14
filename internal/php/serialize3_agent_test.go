package php

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// serialize3AgentTest — инфраструктура для сверки serialize-тестов с живым
// php 8.3. Пишет FILE-секцию рядом с .phpt (как run-tests.php), чтобы
// __FILE__/__DIR__ совпадали у got и у реального php, и печатает unified diff.

func runSerializeReal(p PhpT, phptFile string) (string, error) {
	diag := strings.TrimSuffix(phptFile, ".phpt") + ".php"
	if err := os.WriteFile(diag, []byte(p.File+"\n"), 0644); err != nil {
		return "", err
	}
	defer os.Remove(diag)
	args := []string{"-d", "display_errors=1", "-d", "error_reporting=E_ALL"}
	if p.HasIni {
		for _, ln := range strings.Split(p.Ini, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, ";") || strings.HasPrefix(ln, "[") {
				continue
			}
			args = append(args, "-d", ln)
		}
	}
	args = append(args, diag)
	out, err := exec.Command("php", args...).CombinedOutput()
	return string(out), err
}

// TestSerializeRealDiff проходит PHPT_DIR и печатает got-vs-php для провалов.
//
//	PHPT_DIR=... SERIALIZE_REAL_OUT=/tmp/x.txt go test -run TestSerializeRealDiff -v ./internal/php/
func TestSerializeRealDiff(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	if root == "" {
		t.Skip("PHPT_DIR не задан")
	}
	outPath := os.Getenv("SERIALIZE_REAL_OUT")
	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), "serialize_real.txt")
	}
	fh, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	w := bufio.NewWriter(fh)
	defer w.Flush()

	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
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
		got, rerr := runPhptAt(p, f)
		if t := throwableOf(rerr); t != nil {
			rerr = nil
		}
		g := stripStartup(strings.TrimSpace(got))
		ok := rerr == nil
		want := p.Expect
		if p.HasExpectf {
			want = p.Expectf
		}
		want = stripStartup(strings.TrimSpace(want))
		if ok {
			if p.HasExpect {
				ok = g == want
			}
			if ok && p.HasExpectf {
				ok = expectfToRegex(want).MatchString(g)
			}
		}
		if ok {
			pass++
			continue
		}
		fail++
		real, _ := runSerializeReal(p, f)
		r := normalizePHPOutput(stripStartup(strings.TrimSpace(real)))
		fmt.Fprintf(w, "\n===== FAIL %s =====\n", f)
		if rerr != nil {
			if th := throwableOf(rerr); th != nil {
				fmt.Fprintf(w, "THROW: %s\n", th.Message)
			} else {
				fmt.Fprintf(w, "RUNERR: %v\n", rerr)
			}
		}
		fmt.Fprintf(w, "--- GOT vs PHP ---\n%s\n", unifiedDiff(r, g))
		fmt.Fprintf(w, "--- GOT(full) ---\n%s\n--- WANT(template) ---\n%s\n", g, want)
	}
	fmt.Fprintf(w, "\nTOTAL pass=%d fail=%d skip=%d\n", pass, fail, skip)
	t.Logf("serialize-realdiff pass=%d fail=%d skip=%d out=%s", pass, fail, skip, outPath)
}

// normalizePHPOutput снимает префикс "PHP " у диагностик CLI (run-tests.php
// делает то же самое перед сравнением).
func normalizePHPOutput(s string) string {
	r := strings.NewReplacer(
		"PHP Deprecated: ", "Deprecated: ",
		"PHP Warning: ", "Warning: ",
		"PHP Notice: ", "Notice: ",
		"PHP Fatal error: ", "Fatal error: ",
	)
	return r.Replace(s)
}

// unifiedDiff печатает построчный LCS-diff a (эталон/php) -> b (got).
func unifiedDiff(a, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	// LCS-таблица; объёмы маленькие (serialize-тесты).
	n, m := len(al), len(bl)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var sb strings.Builder
	i, j := 0, 0
	hunk := 0
	var out []string
	for i < n || j < m {
		if i < n && j < m && al[i] == bl[j] {
			out = append(out, "  "+al[i])
			i++
			j++
			continue
		}
		if i >= n {
			out = append(out, "GOT < "+bl[j])
			j++
			continue
		}
		if j >= m {
			out = append(out, "PHP > "+al[i])
			i++
			continue
		}
		if dp[i][j+1] >= dp[i+1][j] {
			out = append(out, "PHP > "+al[i])
			i++
		} else {
			out = append(out, "GOT < "+bl[j])
			j++
		}
	}
	// оставляем только окрестности расхождений
	for k := 0; k < len(out); k++ {
		if !strings.HasPrefix(out[k], "  ") {
			lo := k - 3
			if lo < 0 {
				lo = 0
			}
			hi := k + 8
			if hi > len(out) {
				hi = len(out)
			}
			if hunk > 0 {
				sb.WriteString("  ...\n")
			}
			for ; lo < hi; lo++ {
				sb.WriteString(out[lo] + "\n")
			}
			hunk++
			k = hi - 1
			if hunk >= 2 {
				break
			}
		}
	}
	if hunk == 0 {
		return "  (identical)"
	}
	return strings.TrimRight(sb.String(), "\n")
}

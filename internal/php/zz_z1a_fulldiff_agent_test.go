package php

// zz_z1a_fulldiff_agent_test.go — ВРЕМЕННЫЙ хелпер агента z1_bug_a.
// Полные построчные диффы got/want и got/php для списка тестов.
// Удаляется перед коммитом.
//
//	PHPT_DIR=/corpus PHPT_LIST=/list FULLDIFF_OUT=/out.txt go test -run TestZZFullDiff

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lcsDiff возвращает построчный diff: строки "  x" (общие), "- x" (только got), "+ x" (только want).
func lcsDiff(got, want string) string {
	a := strings.Split(got, "\n")
	b := strings.Split(want, "\n")
	n, m := len(a), len(b)
	// dp[i][j] = длина LCS a[i:], b[j:]
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
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
	for i < n && j < m {
		if a[i] == b[j] {
			sb.WriteString("  " + a[i] + "\n")
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			sb.WriteString("- " + a[i] + "\n")
			i++
		} else {
			sb.WriteString("+ " + b[j] + "\n")
			j++
		}
	}
	for ; i < n; i++ {
		sb.WriteString("- " + a[i] + "\n")
	}
	for ; j < m; j++ {
		sb.WriteString("+ " + b[j] + "\n")
	}
	return sb.String()
}

func runRealAt(p PhpT, file string) (string, error) {
	diag := strings.TrimSuffix(file, ".phpt") + ".php"
	if werr := os.WriteFile(diag, []byte(p.File+"\n"), 0644); werr != nil {
		return "", werr
	}
	defer os.Remove(diag)
	if root := phptRepoRoot(file); root != "" {
		if wd, werr := os.Getwd(); werr == nil && wd != root {
			if cerr := os.Chdir(root); cerr == nil {
				defer os.Chdir(wd)
			}
		}
	}
	args := []string{"-d", "display_errors=1", "-d", "error_reporting=E_ALL"}
	if p.Ini != "" {
		for _, ln := range strings.Split(p.Ini, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "[") {
				continue
			}
			args = append(args, "-d", ln)
		}
	}
	args = append(args, diag)
	out, err := exec.Command("php", args...).CombinedOutput()
	return string(out), err
}

func TestZZFullDiff(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	lp := os.Getenv("PHPT_LIST")
	if root == "" || lp == "" {
		t.Skip("PHPT_DIR/PHPT_LIST не заданы")
	}
	outPath := os.Getenv("FULLDIFF_OUT")
	if outPath == "" {
		outPath = os.Getenv("ROOT_TAIL_OUT")
	}
	if outPath == "" {
		outPath = "/tmp/z1a/fulldiff.txt"
	}
	data, err := os.ReadFile(lp)
	if err != nil {
		t.Fatal(err)
	}
	fh, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	w := bufio.NewWriter(fh)
	defer w.Flush()
	files := []string{}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		files = append(files, ln)
	}
	for _, rel := range files {
		f := rel
		if !filepath.IsAbs(f) {
			f = filepath.Join(root, rel)
		}
		data, rerr := os.ReadFile(f)
		if rerr != nil {
			fmt.Fprintf(w, "\n##### MISS %s: %v\n", f, rerr)
			continue
		}
		p := parsePhpt(string(data))
		got, err2 := runPhptAt(p, f)
		g := strings.TrimSpace(stripStartup(got))
		want := p.Expect
		if p.HasExpectf {
			want = p.Expectf
		}
		want = strings.TrimSpace(stripStartup(want))
		fmt.Fprintf(w, "\n##### %s\n", filepath.Base(f))
		if err2 != nil {
			if th := throwableOf(err2); th != nil {
				fmt.Fprintf(w, "THROW: %s\n", th.Message)
			} else {
				fmt.Fprintf(w, "RUNERR: %v\n", err2)
			}
		}
		fmt.Fprintf(w, "--- diff got(-)=наше want(+)=ожидаемое ---\n%s", lcsDiff(g, want))
		if real, rerr := runRealAt(p, f); rerr == nil {
			real = strings.TrimSpace(stripStartup(real))
			fmt.Fprintf(w, "--- diff got(-)=наше php(+)=живой ---\n%s", lcsDiff(g, real))
		} else {
			fmt.Fprintf(w, "--- php: ошибка: %v (%s)\n", rerr, strings.TrimSpace(stripStartup(string(mustOut(rerr)))))
		}
	}
	t.Logf("fulldiff out=%s", outPath)
}

func mustOut(err error) []byte {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.Stderr
	}
	return nil
}

// TestZZSnip прогоняет снипет из PHPT_SNIP нашем VM и живым php.
func TestZZSnip(t *testing.T) {
	src := os.Getenv("PHPT_SNIP")
	if src == "" {
		t.Skip("PHPT_SNIP не задан")
	}
	got, err := runFile(src)
	fmt.Printf("===== OUR =====\n%s\n--- err=%v\n", got, err)
	f, _ := os.CreateTemp("", "zzsnip-*.php")
	f.WriteString(src)
	f.Close()
	defer os.Remove(f.Name())
	out, perr := exec.Command("php", "-d", "display_errors=1", "-d", "error_reporting=E_ALL", f.Name()).CombinedOutput()
	fmt.Printf("===== PHP =====\n%s\n--- err=%v\n", out, perr)
}

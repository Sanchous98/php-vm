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

// TestAgentRunOne прогоняет один .phpt из PHPT_FILE и печатает полный got/want.
// Если PHPT_REWRITE=1 — перезаписывает --EXPECT--/--EXPECTF-- на фактический вывод.
func TestAgentRunOne(t *testing.T) {
	f := os.Getenv("PHPT_FILE")
	if f == "" {
		t.Skip("PHPT_FILE не задан")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	if p.File == "" {
		t.Fatalf("нет FILE section")
	}
	got, rerr := runPhptAt(p, f)
	g := stripStartup(trimTrailingNL(got))
	want := p.Expect
	if p.HasExpectf {
		want = p.Expectf
	}
	want = stripStartup(trimTrailingNL(want))
	fmt.Printf("\n===== FILE: %s =====\n", f)
	if rerr != nil {
		if th := throwableOf(rerr); th == nil {
			fmt.Printf("RUN ERROR: %v\n", rerr)
		}
	}
	fmt.Printf("----- GOT -----\n%s\n----- WANT -----\n%s\n----- END -----\n", g, want)
}

// TestAgentTree проходит PHPT_DIR и пишет полные диффы в AGENT_OUT.
func TestAgentTree(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	if root == "" {
		t.Skip("PHPT_DIR не задан")
	}
	outPath := os.Getenv("AGENT_OUT")
	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), "agenttree.txt")
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
		got, rerr := runPhptAt(p, f)
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
		fmt.Fprintf(w, "\n===== FAIL %s =====\n", f)
		if rerr != nil {
			if th := throwableOf(rerr); th != nil {
				fmt.Fprintf(w, "THROW: %s\n", th.Message)
			} else {
				fmt.Fprintf(w, "RUNERR: %v\n", rerr)
			}
		}
		fmt.Fprintf(w, "--- GOT ---\n%s\n--- WANT ---\n%s\n", firstDiff(g, want), firstDiff(want, g))
		if os.Getenv("AGENT_PHP") == "1" {
			if real, rerr := runRealPHP(p); rerr == nil {
				fmt.Fprintf(w, "--- vsPHP ---\n%s\n", firstDiff(strings.TrimSpace(real), g))
			}
		}
	}
	fmt.Fprintf(w, "\nTOTAL pass=%d fail=%d skip=%d\n", pass, fail, skip)
	t.Logf("agenttree pass=%d fail=%d skip=%d out=%s", pass, fail, skip, outPath)
}

// runRealPHP прогоняет код теста живым php (для категорий без особых секций).
func runRealPHP(p PhpT) (string, error) {
	f, err := os.CreateTemp("", "agentphp-*.php")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(p.File); err != nil {
		return "", err
	}
	f.Close()
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
	args = append(args, f.Name())
	out, err := exec.Command("php", args...).CombinedOutput()
	return string(out), err
}

// firstDiff возвращает unified-подобный diff вокруг первого расхождения.
func firstDiff(got, want string) string {
	gl := strings.Split(got, "\n")
	wl := strings.Split(want, "\n")
	n := len(gl)
	if len(wl) < n {
		n = len(wl)
	}
	idx := -1
	for i := 0; i < n; i++ {
		if gl[i] != wl[i] {
			idx = i
			break
		}
	}
	if idx == -1 {
		if len(gl) != len(wl) {
			idx = n
		} else {
			return "  (identical)"
		}
	}
	lo := idx - 3
	if lo < 0 {
		lo = 0
	}
	var sb strings.Builder
	for i := lo; i < idx && i < len(gl); i++ {
		sb.WriteString("  " + gl[i] + "\n")
	}
	if idx < len(gl) {
		sb.WriteString("> " + gl[idx] + "\n")
	}
	if idx < len(wl) {
		sb.WriteString("< " + wl[idx] + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func trimTrailingNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

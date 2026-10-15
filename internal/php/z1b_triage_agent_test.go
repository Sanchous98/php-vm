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

// z1b_triage_agent_test.go — триаж-хелпер агента z1b (Zend/tests/bug*).
//
//	PHPT_DIR=<корпус> PHPT_LIST=<список> Z1B_OUT=<файл> \
//	  go test -run TestZ1bTriage -v ./internal/php/
//
// Печатает по каждому тесту: результат против EXPECT/EXPECTF и все расхождения
// с живым php (одинаковый файл, поэтому пути совпадают).

func TestZ1bTriage(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	lp := os.Getenv("PHPT_LIST")
	if root == "" || lp == "" {
		t.Skip("PHPT_DIR/PHPT_LIST не заданы")
	}
	outPath := os.Getenv("Z1B_OUT")
	if outPath == "" {
		outPath = filepath.Join(t.TempDir(), "z1b_triage.txt")
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
	fh, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	w := bufio.NewWriter(fh)
	defer w.Flush()

	pass, fail, skip := 0, 0, 0
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		p := parsePhpt(string(raw))
		if p.File == "" || (!p.HasExpect && !p.HasExpectf) || p.HasSkipIf || p.HasExpectRe || p.HasFileExt || hasUnsupportedExt(p.Extensions) {
			skip++
			fmt.Fprintf(w, "SKIP %s\n", f)
			continue
		}
		phpFile := strings.TrimSuffix(f, ".phpt") + ".php"
		got, rerr := runPhptAt(p, f)
		g := stripStartup(strings.TrimSpace(got))
		want := p.Expect
		if p.HasExpectf {
			want = p.Expectf
		}
		ws := stripStartup(strings.TrimSpace(want))
		ok := rerr == nil
		if th := throwableOf(rerr); th != nil {
			ok = true
		}
		if ok && p.HasExpect {
			ok = g == ws
		}
		if ok && p.HasExpectf {
			ok = expectfToRegex(ws).MatchString(g)
		}
		phpWritten := false
		if werr := os.WriteFile(phpFile, []byte(p.File+"\n"), 0644); werr == nil {
			phpWritten = true
		}
		real, realErr := runRealPHPSameFile(p, phpFile)
		if phpWritten {
			os.Remove(phpFile)
		}
		rs := stripStartup(strings.TrimSpace(real))
		if ok {
			pass++
			fmt.Fprintf(w, "PASS %s\n", f)
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
		fmt.Fprintf(w, "--- vs PHP (php first) ---\n%s\n", diffAll(rs, g, 24))
		if realErr != nil {
			fmt.Fprintf(w, "(php exited: %v)\n", realErr)
		}
		if p.HasExpectf {
			fmt.Fprintf(w, "--- GOT(raw) ---\n%s\n--- EXPECTF ---\n%s\n", capLines(g, 40), capLines(ws, 40))
		}
	}
	fmt.Fprintf(w, "\nTOTAL pass=%d fail=%d skip=%d\n", pass, fail, skip)
	t.Logf("z1b triage pass=%d fail=%d skip=%d out=%s", pass, fail, skip, outPath)
}

func capLines(s string, n int) string {
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		return strings.Join(ls[:n], "\n") + fmt.Sprintf("\n... (%d lines more)", len(ls)-n)
	}
	return s
}

// diffAll печатает все построчные расхождения a/b (не более maxLines строк вывода).
func diffAll(a, b string, maxLines int) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	var sb strings.Builder
	used := 0
	n := len(al)
	if len(bl) > n {
		n = len(bl)
	}
	for i := 0; i < n && used < maxLines; i++ {
		var av, bv string
		if i < len(al) {
			av = al[i]
		}
		if i < len(bl) {
			bv = bl[i]
		}
		if av == bv {
			continue
		}
		// контекст
		for j := i - 1; j <= i+1 && used < maxLines; j++ {
			if j < 0 || j >= n {
				continue
			}
			var a2, b2 string
			if j < len(al) {
				a2 = al[j]
			}
			if j < len(bl) {
				b2 = bl[j]
			}
			mark := " "
			if j == i {
				mark = "!"
			}
			if a2 == b2 {
				fmt.Fprintf(&sb, "%s   %s\n", mark, a2)
			} else {
				fmt.Fprintf(&sb, "%s php %s\n%s our %s\n", mark, a2, mark, b2)
			}
			used++
		}
		sb.WriteString("  ---\n")
	}
	if sb.Len() == 0 && a == b {
		sb.WriteString("  (identical to php)\n")
	}
	return sb.String()
}

// runRealPHPSameFile запускает живой php на уже выложенном файле теста.
func runRealPHPSameFile(p PhpT, phpFile string) (string, error) {
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
	args = append(args, phpFile)
	out, err := exec.Command("php", args...).CombinedOutput()
	return string(out), err
}

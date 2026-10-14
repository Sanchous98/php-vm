package php

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"phpvm/internal/vm"
)

func runFile(src string) (string, error) { return runFileAt(src, "") }

func runFileAt(src, file string) (out string, err error) {
	c, err := CompileFile(src, file)
	if err != nil {
		return "", err
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("vm panic: %v", r)
		}
	}()
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetStepLimit(5000000)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	err = m.Run()
	return buf.String(), err
}

// TestPHPDir прогоняет все .phpt из testdata/phpt и печатает процент.
func TestPHPDir(t *testing.T) {
	dir := "testdata/phpt"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("нет %s", dir)
	}
	pass, total, skip := 0, 0, 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".phpt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		src := string(data)
		p := parsePhpt(src)
		if p.File == "" || (!p.HasExpect && !p.HasExpectf) || p.HasSkipIf || p.HasExpectRe {
			skip++
			continue
		}
		total++
		got, err := runPhptAt(p, filepath.Join(dir, name))
		ok := err == nil
		if ok && p.HasExpect {
			ok = stripStartup(got) == stripStartup(p.Expect)
		}
		if ok && p.HasExpectf {
			ok = expectfMatch(stripStartup(p.Expectf), stripStartup(got))
		}
		if ok {
			pass++
		}
	}
	if total > 0 {
		t.Logf("phpt (php-src): PASSED %d / %d  (%.1f%%), skipped %d", pass, total, 100*float64(pass)/float64(total), skip)
	} else {
		t.Logf("phpt: нечего запускать (skipped %d)", skip)
	}
}

// TestPHPTree рекурсивно прогоняет .phpt из каталога PHPT_DIR.
//
//	PHPT_DIR=/path PHPT_LIMIT=2000 go test -run TestPHPTree -v ./internal/php/
func TestPHPTree(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	if root == "" {
		t.Skip("PHPT_DIR не задан")
	}
	var slowLog *os.File
	if p := os.Getenv("PHPT_SLOWLOG"); p != "" {
		slowLog, _ = os.Create(p)
		if slowLog != nil {
			defer slowLog.Close()
			slowLog.Sync()
		}
	}
	limit := 0
	if v := os.Getenv("PHPT_LIMIT"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	maxSamples := 60
	if v := os.Getenv("PHPT_MAXSAMPLES"); v != "" {
		maxSamples, _ = strconv.Atoi(v)
	}
	offset := 0
	if v := os.Getenv("PHPT_OFFSET"); v != "" {
		offset, _ = strconv.Atoi(v)
	}
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	// PHPT_LIST — файл с подстроками путей (по одной в строке): запускать только
	// тесты, чей путь содержит хотя бы одну из них (для параллельных прогонов).
	if lp := os.Getenv("PHPT_LIST"); lp != "" {
		if data, rerr := os.ReadFile(lp); rerr == nil {
			var pats []string
			for _, ln := range strings.Split(string(data), "\n") {
				if ln = strings.TrimSpace(ln); ln != "" {
					pats = append(pats, ln)
				}
			}
			if len(pats) > 0 {
				var kept []string
				for _, f := range files {
					for _, pat := range pats {
						if strings.Contains(f, pat) {
							kept = append(kept, f)
							break
						}
					}
				}
				files = kept
			}
		}
	}
	if offset > 0 && offset < len(files) {
		files = files[offset:]
	} else if offset >= len(files) {
		files = nil
	}
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}

	type bucket struct{ pass, fail int }
	byCat := map[string]*bucket{}
	pass, fail, skip := 0, 0, 0
	var samples []string
	for _, f := range files {
		data, _ := os.ReadFile(f)
		p := parsePhpt(string(data))
		if p.HasSkipIf || p.HasExpectRe || p.HasFileExt || p.File == "" || (!p.HasExpect && !p.HasExpectf) {
			skip++
			continue
		}
		if hasUnsupportedExt(p.Extensions) {
			// Тест требует расширение, которого нет в этой VM (zend_test/FFI/GMP
			// и т.п.) — как run-tests.php, пропускаем.
			skip++
			continue
		}
		if excl := os.Getenv("PHPT_EXCLUDE"); excl != "" {
			bad := false
			for _, s := range strings.Split(excl, ",") {
				if s != "" && strings.Contains(p.File, s) {
					bad = true
					break
				}
			}
			if bad {
				skip++
				continue
			}
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
				skip++
				continue
			}
		}
		if os.Getenv("PHPT_NO_OBJECTS") != "" && containsAny(p.File,
			[]string{"class ", "new ", "->", "::", "interface ", "trait ", "instanceof", "extends ", "implements "}) {
			skip++
			continue
		}
		cat := categoryOf(root, f)
		b := byCat[cat]
		if b == nil {
			b = &bucket{}
			byCat[cat] = b
		}
		start := time.Now()
		got, err := runPhptAt(p, f)
		if slowLog != nil {
			if d := time.Since(start); d > 2*time.Second {
				fmt.Fprintf(slowLog, "SLOW %v %s\n", d, f)
			}
		}
		if t := throwableOf(err); t != nil {
			// непойманное исключение: fatal уже напечатан в вывод — сравниваем вывод
			err = nil
		}
		ok := err == nil
		if ok {
			// php-src run-tests.php нормализует переводы строк в выводе
			// (CRLF -> LF) перед сравнением с EXPECT.
			raw := stripStartup(strings.ReplaceAll(got, "\r\n", "\n"))
			g := stripStartup(phpTrim(raw))
			if p.HasExpect {
				ok = g == stripStartup(phpTrim(p.Expect))
			}
			if ok && p.HasExpectf {
				w := stripStartup(phpTrim(p.Expectf))
				// Шаблон, оканчивающийся на %A, «поглощает» хвостовой перевод
				// строки вывода: сопоставляем без trim (как php-src: %A матчит
				// и \n).
				if strings.HasSuffix(strings.TrimRight(w, " \t"), "%A") {
					// %A поглощает и хвостовой перевод строки, поэтому
					// сравниваем без правого trim, но ведущие переводы строк
					// (префикс диагностики "\n") снимаем, как run-tests.php.
					ok = expectfMatch(w, strings.TrimLeft(raw, "\r\n"))
				} else {
					ok = expectfMatch(w, g)
				}
			}
		}
		if ok {
			pass++
			b.pass++
		} else {
			fail++
			b.fail++
			if len(samples) < maxSamples {
				why := "mismatch"
				if err != nil {
					why = "run: " + err.Error()
				} else {
					want := p.Expect
					if p.HasExpectf {
						want = p.Expectf
					}
					if os.Getenv("PHPT_DIFFSIG") != "" {
						gotT := stripStartup(phpTrim(stripStartup(strings.ReplaceAll(got, "\r\n", "\n"))))
						if p.HasExpectf {
							why = "mismatch\n" + expectfLineDiff(stripStartup(phpTrim(want)), gotT)
						} else {
							why = "mismatch\n" + firstDiff(gotT, stripStartup(phpTrim(want)))
						}
					} else {
						why = "mismatch got=" + strconv.Quote(clip(got)) + " want=" + strconv.Quote(clip(want))
					}
				}
				samples = append(samples, f+": "+why)
			}
		}
	}

	var cats []string
	for k := range byCat {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	for _, k := range cats {
		b := byCat[k]
		t.Logf("  %-44s %5d/%5d (%.1f%%)", k, b.pass, b.pass+b.fail, 100*float64(b.pass)/float64(b.pass+b.fail))
	}
	if pass+fail > 0 {
		t.Logf("ИТОГО: %d / %d (%.1f%%), пропущено %d", pass, pass+fail, 100*float64(pass)/float64(pass+fail), skip)
	}
	for _, s := range samples {
		t.Logf("  FAIL %s", s)
	}
}

// phpTrim повторяет trim() из run-tests.php: по умолчанию срезает также
// NUL-байты и вертикальную табуляцию.
func phpTrim(s string) string {
	return strings.Trim(s, " \t\n\r\x00\x0B")
}

// expandIniPlaceholders повторяет подстановки run-tests.php для секции --INI--:
// {PWD}, {TMP} и {MAIL:файл} — последний превращается в "tee файл >/dev/null"
// (в run-tests.php это preg_replace, где $1 заменяется именем файла).
func expandIniPlaceholders(ini, file string) string {
	if ini == "" {
		return ini
	}
	ini = strings.ReplaceAll(ini, "{PWD}", filepath.Dir(file))
	ini = strings.ReplaceAll(ini, "{TMP}", os.TempDir())
	for {
		i := strings.Index(ini, "{MAIL:")
		if i < 0 {
			break
		}
		j := i + len("{MAIL:")
		k := j
		for k < len(ini) && ini[k] != '}' && !isSpaceIniByte(ini[k]) {
			k++
		}
		if k >= len(ini) || ini[k] != '}' {
			break
		}
		ini = ini[:i] + "tee " + ini[j:k] + " >/dev/null" + ini[k+1:]
	}
	return ini
}

func isSpaceIniByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// unsupportedExtensions — расширения php-src, которые эта VM не реализует;
// тесты с ними пропускаются (аналог проверки extension_loaded в run-tests.php).
var unsupportedExtensions = map[string]bool{
	"zend_test":  true,
	"ffi":        true,
	"gmp":        true,
	"com_dotnet": true,
}

// hasUnsupportedExt сообщает, требует ли тест нереализованное расширение.
func hasUnsupportedExt(exts []string) bool {
	for _, e := range exts {
		if unsupportedExtensions[strings.ToLower(e)] {
			return true
		}
	}
	return false
}

func categoryOf(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "?"
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return parts[0]
}

func clip(s string) string {
	limit := 120
	if v := os.Getenv("PHPT_CLIP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}

// containsAny сообщает, содержит ли строка любую из подстрок.
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// throwableOf извлекает *vm.Throwable из ошибки (непойманное исключение).
func throwableOf(err error) *vm.Throwable {
	if err == nil {
		return nil
	}
	if t, ok := err.(*vm.Throwable); ok {
		return t
	}
	return nil
}

// expectfLineDiff ищет первую строку вывода, не совпавшую с EXPECTF-шаблоном
// (построчное сопоставление — для компактной диагностики в логах).
func expectfLineDiff(want, got string) string {
	wl := strings.Split(strings.TrimRight(want, "\n"), "\n")
	gl := strings.Split(strings.TrimRight(got, "\n"), "\n")
	n := len(wl)
	if len(gl) > n {
		n = len(gl)
	}
	for i := 0; i < n; i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if !expectfMatch(w, g) {
			return fmt.Sprintf("line %d:\n  got=%q\n  want=%q", i+1, g, w)
		}
	}
	return "  (expectf lines all match)"
}

package php

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"phpvm/internal/vm"
)

// installSuperglobals заполняет суперглобалы VM из секций .phpt-теста.
//
// Реализация намеренно живёт в php-пакете (не в vm): суперглобалы — это просто
// массивы, положенные в глобальную таблицу VM, которую main-скоуп читает/пишет
// через OpGlobalGet/OpGlobalSet.
func installSuperglobals(m *vm.VM, p PhpT, file string) {
	cfg := parseInputConfig(p.Ini)
	SetIniString(p.Ini)
	m.SetIniLookup(IniValue)
	// --INI-- error_reporting (возможно, выражение с константами E_*)
	// применяем к VM: без этого диагностики фильтруются по E_ALL.
	if v, ok := IniValue("error_reporting"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			m.ErrorReportingSet(n)
		}
	}
	// --STDIN--: данные стандартного ввода (run-tests сохраняет завершающий
	// перевод строки секции).
	if p.HasStdin {
		SetStdinData([]byte(p.Stdin + "\n"))
	} else {
		SetStdinData(nil)
	}
	// php-src печатает стартовые Deprecated для устаревших INI-директив
	// (включённых через --INI---/-d), с локацией "Unknown on line 0".
	for _, d := range []struct{ key, msg string }{
		{"auto_detect_line_endings", "auto_detect_line_endings is deprecated"},
		{"allow_url_include", "Directive 'allow_url_include' is deprecated"},
	} {
		if v, ok := IniValue(d.key); ok && iniBool(v) {
			m.Write("\nDeprecated: " + d.msg + " in Unknown on line 0\n")
		}
	}
	// assert.* — устаревшие INI-директивы (PHP 8.3): стартовый Deprecated,
	// если значение в --INI-- отличается от дефолта (порядок регистрации:
	// active, warning, bail, callback, exception).
	{
		raw := parseIniSettings(p.Ini)
		for _, d := range []struct{ key, def, kind string }{
			{"assert.active", "1", "bool"},
			{"assert.warning", "1", "bool"},
			{"assert.bail", "0", "bool"},
			{"assert.callback", "", "str"},
			{"assert.exception", "1", "bool"},
		} {
			v, ok := raw[d.key]
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			if d.kind == "bool" {
				if iniBoolInt(v) == iniBoolInt(d.def) {
					continue
				}
			} else if v == strings.TrimSpace(d.def) {
				continue
			}
			m.Write("\nDeprecated: PHP Startup: " + d.key + " INI setting is deprecated in Unknown on line 0\n")
		}
	}
	// mbstring.internal_encoding — устаревшая директива: PHP печатает
	// стартовый Deprecated при любом непустом значении.
	if v, ok := IniValue("mbstring.internal_encoding"); ok && v != "" {
		m.Write("\nDeprecated: PHP Startup: Use of mbstring.internal_encoding is deprecated in Unknown on line 0\n")
	}
	// --INI-- error_reporting=N|expr влияет на уровень диагностик VM.
	if v, ok := IniValue("error_reporting"); ok {
		if lvl, ok := parseErrorReportingValue(v); ok {
			m.ErrorReportingSet(lvl)
		}
	}

	get := vm.NewArray()
	if cfg.hasVar('G') {
		parseURLInto(get, p.Get, &cfg)
	}

	// Тело запроса: --POST_RAW-- (с заголовками) либо --POST-- (urlencoded).
	var contentType, body string
	hasBody := false
	switch {
	case p.HasPostRaw:
		ctype, b := splitRawPost(p.PostRaw)
		contentType, body, hasBody = ctype, b, true
	case p.HasPost:
		contentType, body, hasBody = "application/x-www-form-urlencoded", p.Post, true
	}

	post, files := vm.NewArray(), vm.NewArray()
	var warns []string
	SetRawInput(body)
	if hasBody && cfg.enablePostDataReading {
		if cfg.postMaxSize > 0 && int64(len(body)) > cfg.postMaxSize {
			warns = append(warns, postTooLargeWarning(int64(len(body)), cfg.postMaxSize))
		} else if strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data") {
			var ws []string
			post, files, ws = parseUploadBody(contentType, body, &cfg)
			warns = append(warns, ws...)
		} else if cfg.hasVar('P') {
			parseURLInto(post, body, &cfg)
		}
	}

	cookie := vm.NewArray()
	if cfg.hasVar('C') {
		parseCookiesInto(cookie, p.Cookie)
	}

	env := vm.NewArray()
	if p.HasEnv && cfg.hasVar('E') {
		fillKV(env, p.Env)
	}
	// CLI наполняет $_ENV из окружения процесса при variables_order, включающем E.
	if cfg.hasVar('E') {
		for _, name := range []string{"TEST_PHP_EXECUTABLE", "TEST_PHP_EXECUTABLE_ESCAPED"} {
			if v := os.Getenv(name); v != "" {
				env.SetStr(name, vm.MakeString(v))
			}
		}
	}

	server := vm.NewArray()
	buildServer(m, server, p, file, &cfg, contentType, body, hasBody)

	// $_REQUEST в порядке request_order (по умолчанию производная variables_order).
	request := vm.NewArray()
	for _, ch := range cfg.requestOrder {
		switch ch {
		case 'G':
			mergeInto(request, get)
		case 'P':
			mergeInto(request, post)
		case 'C':
			mergeInto(request, cookie)
		}
	}

	if cfg.warnedVars {
		warns = append(warns, maxInputVarsWarning(cfg.maxInputVars))
	}

	globals := map[string]*vm.Array{
		"$_GET":     get,
		"$_POST":    post,
		"$_COOKIE":  cookie,
		"$_ENV":     env,
		"$_SERVER":  server,
		"$_REQUEST": request,
		"$_FILES":   files,
	}
	for name, arr := range globals {
		m.GlobalSet(name, vm.MakeArray(arr))
	}

	// $GLOBALS — упрощённый снимок текущей глобальной таблицы (суперглобалы
	// плюс $argv/$argc). Полноценная живая синхронизация не делается.
	g := vm.NewArray()
	for _, name := range []string{"$_ENV", "$_GET", "$_POST", "$_COOKIE", "$_SERVER", "$_REQUEST", "$_FILES"} {
		g.SetStr(name[1:], vm.MakeArray(globals[name]))
	}
	if v, ok := m.GlobalGet("$argv"); ok {
		g.SetStr("argv", v)
	}
	if v, ok := m.GlobalGet("$argc"); ok {
		g.SetStr("argc", v)
	}
	m.GlobalSet("$GLOBALS", vm.MakeArray(g))

	// Предупреждения стадии request startup печатаются перед выводом скрипта.
	for _, w := range warns {
		m.Write(w)
	}
}

// buildServer заполняет $_SERVER. argc/argv берутся из --ARGS-- либо, в
// CLI-режиме, из GET-строки без '='; для POST_RAW добавляются CONTENT_*,
// QUERY_STRING и HTTP_* заголовки.
func buildServer(m *vm.VM, server *vm.Array, p PhpT, file string, cfg *inputConfig, contentType, body string, hasBody bool) {
	// argv/argc живут глобально независимо от variables_order, но попадают в
	// $_SERVER только при наличии 'S'.
	if cfg.registerArgcArgv {
		var argv []string
		switch {
		case p.HasArgs:
			argv = append([]string{file}, strings.Fields(p.Args)...)
		case p.HasGet && p.Get != "" && !strings.Contains(p.Get, "="):
			argv = strings.Split(p.Get, "+")
		}
		if argv != nil {
			arr := vm.NewArray()
			for _, a := range argv {
				_, _ = arr.Append(vm.MakeString(a))
			}
			m.GlobalSet("$argv", vm.MakeArray(arr))
			m.GlobalSet("$argc", vm.MakeInt(int64(len(argv))))
			if cfg.hasVar('S') {
				server.SetStr("argv", vm.MakeArray(arr))
				server.SetStr("argc", vm.MakeInt(int64(len(argv))))
			}
		}
	}

	if !cfg.hasVar('S') {
		return
	}

	// CLI SAPI: время старта запроса (bug64370_var1).
	now := time.Now()
	server.SetStr("REQUEST_TIME", vm.MakeInt(now.Unix()))
	server.SetStr("REQUEST_TIME_FLOAT", vm.MakeFloat(float64(now.UnixNano())/1e9))

	if file != "" {
		server.SetStr("SCRIPT_FILENAME", vm.MakeString(file))
		server.SetStr("SCRIPT_NAME", vm.MakeString(file))
		server.SetStr("PHP_SELF", vm.MakeString(file))
	}
	if p.HasGet {
		server.SetStr("REQUEST_METHOD", vm.MakeString("GET"))
		server.SetStr("QUERY_STRING", vm.MakeString(p.Get))
	}
	if hasBody {
		server.SetStr("REQUEST_METHOD", vm.MakeString("POST"))
		if contentType != "" {
			server.SetStr("CONTENT_TYPE", vm.MakeString(contentType))
		}
		server.SetStr("CONTENT_LENGTH", vm.MakeInt(int64(len(body))))
	}
	if p.HasPostRaw {
		for k, v := range parseRawHeaders(p.PostRaw) {
			lk := strings.ToLower(k)
			if lk == "content-type" || lk == "content-length" {
				continue
			}
			server.SetStr("HTTP_"+strings.ToUpper(strings.ReplaceAll(k, "-", "_")), vm.MakeString(v))
		}
	}
	if p.HasServer {
		fillKV(server, p.Server)
	}
	// CLI SAPI наполняет $_SERVER переменными окружения (variables_order=...S).
	// Тестовый раннер выставляет --ENV-- в окружении процесса — повторяем это
	// в $_SERVER (нужно get_browser() с HTTP_USER_AGENT и подобным).
	if p.HasEnv {
		for _, line := range strings.Split(strings.ReplaceAll(p.Env, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq < 0 {
				continue
			}
			name := strings.TrimSpace(line[:eq])
			val := line[eq+1:]
			if name == "" || val == "" {
				continue
			}
			server.SetStr(name, vm.MakeString(val))
		}
	}
}

// fillKV наполняет массив из секции вида KEY=value (--ENV--/--SERVER--).
func fillKV(arr *vm.Array, s string) {
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i := strings.IndexByte(line, '='); i >= 0 {
			arr.SetStr(line[:i], vm.MakeString(line[i+1:]))
		}
	}
}

// mergeInto копирует все элементы src в dst (dst уже существует).
func mergeInto(dst, src *vm.Array) {
	for k, v := range src.All() {
		if k.IsStr() {
			dst.SetStr(k.StrVal(), v)
		} else {
			dst.SetInt(k.IntVal(), v)
		}
	}
}

// --- разбор query-строки / urlencoded-тела (parse_str) ---

func parseStrURL(s string) *vm.Array {
	arr := vm.NewArray()
	if s == "" {
		return arr
	}
	for _, pair := range strings.Split(s, "&") {
		if pair == "" {
			continue
		}
		k, v := pair, ""
		if i := strings.IndexByte(pair, '='); i >= 0 {
			k, v = pair[:i], pair[i+1:]
		}
		insertParam(arr, urlDecode(k), vm.MakeString(urlDecode(v)))
	}
	return arr
}

type paramSeg struct {
	append bool
	key    string
}

// insertParam вставляет значение по «PHP-ключу» вида a[][b][0].
func insertParam(root *vm.Array, rawKey string, val vm.Slot) {
	// Имя с '[' без закрывающей ']' PHP трактует как плоскую переменную,
	// заменяя '[' (и пробелы/точки) на '_' (bug #78236).
	if strings.Contains(rawKey, "[") && !strings.Contains(rawKey, "]") {
		arrSet(root, flatVarName(rawKey), val)
		return
	}
	base := rawKey
	var segs []paramSeg
	if i := strings.IndexByte(rawKey, '['); i >= 0 {
		base = rawKey[:i]
		rest := rawKey[i:]
		for len(rest) > 0 && rest[0] == '[' {
			j := strings.IndexByte(rest, ']')
			if j < 0 {
				break
			}
			if c := rest[1:j]; c == "" {
				segs = append(segs, paramSeg{append: true})
			} else {
				segs = append(segs, paramSeg{key: c})
			}
			rest = rest[j+1:]
		}
	}
	base = phpVarName(base)
	if len(segs) == 0 {
		arrSet(root, base, val)
		return
	}
	setNested(root, base, segs, val)
}

func setNested(parent *vm.Array, base string, segs []paramSeg, val vm.Slot) {
	child := childArray(parent, base)
	for i := 0; i < len(segs)-1; i++ {
		seg := segs[i]
		if seg.append {
			next := vm.NewArray()
			_, _ = child.Append(vm.MakeArray(next))
			child = next
		} else {
			child = childArray(child, seg.key)
		}
	}
	last := segs[len(segs)-1]
	if last.append {
		_, _ = child.Append(val)
	} else {
		arrSet(child, last.key, val)
	}
}

// childArray возвращает вложенный массив по ключу, создавая его при отсутствии.
func childArray(parent *vm.Array, key string) *vm.Array {
	if i, ok := canonicalInt(key); ok {
		if v, ok := parent.Get(vm.MakeIntKey(i)); ok && v.Type() == vm.ArrayType {
			return v.AsArray()
		}
		child := vm.NewArray()
		parent.SetInt(i, vm.MakeArray(child))
		return child
	}
	if v, ok := parent.Get(vm.MakeStrKey(key)); ok && v.Type() == vm.ArrayType {
		return v.AsArray()
	}
	child := vm.NewArray()
	parent.SetStr(key, vm.MakeArray(child))
	return child
}

func arrSet(arr *vm.Array, key string, val vm.Slot) {
	if i, ok := canonicalInt(key); ok {
		arr.SetInt(i, val)
		return
	}
	arr.SetStr(key, val)
}

// canonicalInt повторяет правило PHP: каноническое десятичное целое → int-ключ.
func canonicalInt(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	body := s
	if s[0] == '-' {
		body = s[1:]
	}
	if body == "" {
		return 0, false
	}
	if body[0] == '0' && (len(body) > 1 || s[0] == '-') {
		return 0, false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// phpVarName заменяет пробелы и точки в имени переменной на '_'.
func phpVarName(s string) string {
	r := strings.NewReplacer(" ", "_", ".", "_")
	return r.Replace(s)
}

// flatVarName — вариант phpVarName для плоских имён, включающих '['/']'.
func flatVarName(s string) string {
	r := strings.NewReplacer(" ", "_", ".", "_", "[", "_", "]", "_")
	return r.Replace(s)
}

// --- POST_RAW / multipart ---

// splitRawPost отделяет HTTP-заголовки (первая строка Content-Type и т.п.)
// от тела. Тело может начинаться сразу после заголовков без пустой строки.
func splitRawPost(raw string) (contentType, body string) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	i := 0
	for i < len(lines) && httpHeaderRe.MatchString(lines[i]) {
		if strings.HasPrefix(strings.ToLower(lines[i]), "content-type:") {
			contentType = strings.TrimSpace(lines[i][len("content-type:"):])
		}
		i++
	}
	body = strings.Join(lines[i:], "\n")
	return contentType, body
}

// --- декодирование ---

func urlDecode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '+':
			b.WriteByte(' ')
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b.WriteByte(byte(hexNibble(s[i+1])<<4 | hexNibble(s[i+2])))
			i += 2
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func percentDecode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(byte(hexNibble(s[i+1])<<4 | hexNibble(s[i+2])))
			i += 2
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// --- раннер и проверки ---

// runPhptAt компилирует и запускает .phpt с заполнением суперглобалов.
// sharedRunVM переиспользуется между прогонами .phpt, чтобы не выделять
// многомегабайтный VM на каждый из тысяч тестов (иначе полный прогон дерева
// упирается в OOM). Тесты выполняются последовательно.
var sharedRunVM vm.VM

// phptRepoRoot находит корень php-src для файла теста (каталог, содержащий
// подкаталоги ext/Zend), чтобы эмулировать cwd run-tests.php. Пусто — не phpsrc.
func phptRepoRoot(file string) string {
	abs, aerr := filepath.Abs(file)
	if aerr != nil {
		return ""
	}
	dir := filepath.Dir(abs)
	for {
		for _, sub := range []string{"ext", "Zend", "run-tests.php"} {
			if st, serr := os.Stat(filepath.Join(dir, sub)); serr == nil {
				_ = st
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func runPhptAt(p PhpT, file string) (out string, err error) {
	diagFile := file
	if strings.HasSuffix(diagFile, ".phpt") {
		diagFile = strings.TrimSuffix(diagFile, ".phpt") + ".php"
		// Как run-tests.php: выкладываем FILE рядом с .phpt, чтобы __FILE__ и
		// __DIR__ указывали на реально существующий файл (fopen(__FILE__),
		// require(__DIR__."/x.inc") и т.п.). Best-effort, с очисткой.
		// run-tests.php сохраняет секцию FILE вместе с завершающим переводом
		// строки (он был снят парсером), поэтому возвращаем его.
		if werr := os.WriteFile(diagFile, []byte(p.File+"\n"), 0644); werr == nil {
			defer os.Remove(diagFile)
		}
	}
	// run-tests.php запускается из корня php-src, поэтому относительные пути
	// (например, tempnam("./ext/standard/tests/file/")) разрешаются оттуда.
	if root := phptRepoRoot(file); root != "" {
		if wd, werr := os.Getwd(); werr == nil && wd != root {
			if cerr := os.Chdir(root); cerr == nil {
				defer os.Chdir(wd)
			}
		}
	}
	// --INI-- применяем до компиляции: часть настроек (zend.multibyte) влияет
	// на поведение компилятора. Повторное применение в installSuperglobals
	// идемпотентно.
	p.Ini = expandIniPlaceholders(p.Ini, file)
	SetIniString(p.Ini)
	// --INI--: применяем влияющие на разбор настройки (short_open_tag).
	if p.HasIni {
		for _, line := range strings.Split(p.Ini, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, ";") {
				continue
			}
			kv := strings.SplitN(line, "=", 2)
			if len(kv) != 2 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(kv[0]), "short_open_tag") {
				v := strings.ToLower(strings.TrimSpace(kv[1]))
				old := ShortOpenTag
				ShortOpenTag = v == "on" || v == "1" || v == "true" || v == "yes"
				defer func() { ShortOpenTag = old }()
			}
		}
	}
	c, err := CompileFile(p.File+"\n", diagFile)
	if err != nil {
		return "", err
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("vm panic: %v", r)
		}
	}()
	m := &sharedRunVM
	m.Init(c.Main, c.Funcs, c.Classes)
	// disable_classes: применяем к свежему Init (INI уже разобран выше).
	if v, ok := iniValues["disable_classes"]; ok {
		m.SetDisabledClasses(strings.Split(v, ","))
	} else {
		m.SetDisabledClasses(nil)
	}
	// fiber.stack_size из --INI-- действует на start() (gh20483).
	if v, ok := iniValues["fiber.stack_size"]; ok {
		m.SetFiberStackSize(v)
	}
	m.SetStepLimit(5000000)
	ResetResourceState()
	// Как run-tests.php: путь к PHP-бинарнику для тестов, запускающих
	// подпроцессы (exec/system).
	if _, ok := os.LookupEnv("TEST_PHP_EXECUTABLE"); !ok {
		if phpPath, lerr := exec.LookPath("php"); lerr == nil {
			_ = os.Setenv("TEST_PHP_EXECUTABLE", phpPath)
			_ = os.Setenv("TEST_PHP_EXECUTABLE_ESCAPED", "'"+phpPath+"'")
		}
	}
	var buf bytes.Buffer
	m.SetOutput(&buf)
	installSuperglobals(m, p, file)
	// --ENV-- как в run-tests.php: переменные реально выставляются в окружении
	// процесса (getenv/zend_getenv), с восстановлением после прогона.
	type envSaved struct {
		name string
		old  string
		had  bool
	}
	var envRestore []envSaved
	if p.HasEnv {
		for _, line := range strings.Split(p.Env, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, ";") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq < 0 {
				continue
			}
			name := strings.TrimSpace(line[:eq])
			val := line[eq+1:]
			// proc_open в run-tests.php не выставляет пустые значения env;
			// эмулируем это, чтобы ${var:-fallback} вёл себя как в php-src.
			if val == "" {
				continue
			}
			old, had := os.LookupEnv(name)
			envRestore = append(envRestore, envSaved{name, old, had})
			_ = os.Setenv(name, val)
		}
	}
	err = m.Run()
	for i := len(envRestore) - 1; i >= 0; i-- {
		if envRestore[i].had {
			_ = os.Setenv(envRestore[i].name, envRestore[i].old)
		} else {
			_ = os.Unsetenv(envRestore[i].name)
		}
	}
	if p.HasClean {
		runPhptClean(p, diagFile)
	}
	return buf.String(), err
}

// runPhptClean выполняет секцию --CLEAN-- (как run-tests.php), чтобы тесты не
// накапливали временные файлы в каталоге корпуса. Best-effort, ошибки игнорим.
func runPhptClean(p PhpT, diagFile string) {
	cc, cerr := CompileFile(p.Clean, diagFile)
	if cerr != nil {
		return
	}
	defer func() { _ = recover() }()
	var cm vm.VM
	cm.Init(cc.Main, cc.Funcs, cc.Classes)
	cm.SetStepLimit(5000000)
	cm.SetOutput(io.Discard)
	_ = cm.Run()
}

func TestSuperglobalsGet(t *testing.T) {
	p := parsePhpt("--GET--\na=1&b[]=2&b[]=3&c[x]=y\n--FILE--\n<?php echo $_GET['a'],'-',$_GET['b'][1],'-',$_GET['c']['x'];")
	if !p.HasGet {
		t.Fatal("GET section not parsed")
	}
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "1-3-y"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsPost(t *testing.T) {
	p := parsePhpt("--POST--\na=Hello+World&list[]=1&list[]=2\n--FILE--\n<?php echo $_POST['a'],'|',count($_POST['list']);")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "Hello World|2"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsMultipart(t *testing.T) {
	raw := "--POST_RAW--\n" +
		"Content-Type: multipart/form-data; boundary=BVoyv\n" +
		"--BVoyv\n" +
		"Content-Disposition: form-data; name=\"data\"\n" +
		"\n" +
		"abc\n" +
		"--BVoyv\n" +
		"Content-Disposition: form-data; name=\"up\"; filename=\"f.txt\"\n" +
		"Content-Type: text/plain\n" +
		"\n" +
		"hello\n" +
		"--BVoyv--\n" +
		"--FILE--\n" +
		"<?php echo $_POST['data'],'|',$_FILES['up']['name'],'|',$_FILES['up']['size'];"
	p := parsePhpt(raw)
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "abc|f.txt|5"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsCookie(t *testing.T) {
	p := parsePhpt("--COOKIE--\nx=1; y=hello%20world; x=dup\n--FILE--\n<?php echo $_COOKIE['x'],'-',$_COOKIE['y'];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "1-hello world"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsArgs(t *testing.T) {
	p := parsePhpt("--ARGS--\nab cd ef\n--FILE--\n<?php echo $argc,'-',$argv[0],',',$argv[1],',',$argv[3],'-',$_SERVER['argc'];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "4-test.phpt,ab,ef-4"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsServerEnv(t *testing.T) {
	p := parsePhpt("--ENV--\nFOO=bar\n--SERVER--\nCUSTOM=x\n--GET--\na=1\n--FILE--\n<?php echo $_ENV['FOO'],'|',$_SERVER['CUSTOM'],'|',$_SERVER['REQUEST_METHOD'];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "bar|x|GET"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// parseErrorReportingValue разбирает значение INI error_reporting (число или
// выражение из констант E_* с операторами |, &, ~ и скобками).
func parseErrorReportingValue(s string) (int64, bool) {
	consts := map[string]int64{
		"E_ERROR": 1, "E_WARNING": 2, "E_PARSE": 4, "E_NOTICE": 8,
		"E_CORE_ERROR": 16, "E_CORE_WARNING": 32, "E_COMPILE_ERROR": 64,
		"E_COMPILE_WARNING": 128, "E_USER_ERROR": 256, "E_USER_WARNING": 512,
		"E_USER_NOTICE": 1024, "E_STRICT": 2048, "E_RECOVERABLE_ERROR": 4096,
		"E_DEPRECATED": 8192, "E_USER_DEPRECATED": 16384, "E_ALL": 32767,
	}
	i := 0
	skip := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}
	var expr, unary func() (int64, bool)
	primary := func() (int64, bool) {
		skip()
		if i < len(s) && s[i] == '(' {
			i++
			v, ok := expr()
			if !ok {
				return 0, false
			}
			skip()
			if i >= len(s) || s[i] != ')' {
				return 0, false
			}
			i++
			return v, true
		}
		start := i
		for i < len(s) && (s[i] == '_' || (s[i] >= 'A' && s[i] <= 'Z') ||
			(s[i] >= 'a' && s[i] <= 'z') || (s[i] >= '0' && s[i] <= '9')) {
			i++
		}
		tok := s[start:i]
		if tok == "" {
			return 0, false
		}
		if v, ok := consts[strings.ToUpper(tok)]; ok {
			return v, true
		}
		n, err := strconv.ParseInt(tok, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	unary = func() (int64, bool) {
		skip()
		if i < len(s) && s[i] == '~' {
			i++
			v, ok := unary()
			if !ok {
				return 0, false
			}
			return ^v, true
		}
		return primary()
	}
	term := func() (int64, bool) {
		v, ok := unary()
		if !ok {
			return 0, false
		}
		for {
			skip()
			if i < len(s) && s[i] == '&' {
				i++
				r, ok := unary()
				if !ok {
					return 0, false
				}
				v &= r
				continue
			}
			return v, true
		}
	}
	expr = func() (int64, bool) {
		v, ok := term()
		if !ok {
			return 0, false
		}
		for {
			skip()
			if i < len(s) && s[i] == '|' {
				i++
				r, ok := term()
				if !ok {
					return 0, false
				}
				v |= r
				continue
			}
			return v, true
		}
	}
	v, ok := expr()
	skip()
	if !ok || i != len(s) {
		return 0, false
	}
	return v, true
}

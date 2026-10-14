package php

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// Этот файл расширяет заполнение суперглобалов .phpt-раннера:
//   - разбор --INI-- (variables_order/register_argc_argv/post_max_size/
//     max_input_vars/max_input_nesting_level/file_uploads/upload_max_filesize/
//     enable_post_data_reading);
//   - полноценный $_FILES из multipart (вложенные массивы по имени поля);
//   - $_REQUEST в порядке request_order;
//   - $_COOKIE с array-синтаксисом имён;
//   - $_SERVER (CONTENT_TYPE/CONTENT_LENGTH/QUERY_STRING/HTTP_*).
//
// Замечания о том, что применить нельзя (нет соответствующего builtin/VM):
//   - php://input (file_get_contents("php://input")) — не поддерживается VM,
//     поэтому enable_post_data_reading проверяется только на заполнение
//     $_POST/$_FILES;
//   - sys_get_temp_dir()/init_get — builtins, не в этой зоне;
//   - mbstring.encoding_translation (Shift_JIS -> UTF-8) — не поддерживается.

// --- разбор --INI-- ---

type phptIni map[string]string

func parseIniSection(raw string) phptIni {
	m := phptIni{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		m[strings.ToLower(k)] = v
	}
	return m
}

func (in phptIni) raw(k string) (string, bool) {
	v, ok := in[strings.ToLower(k)]
	return v, ok
}

func (in phptIni) getString(k, def string) string {
	if v, ok := in.raw(k); ok {
		return v
	}
	return def
}

func iniBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}

func (in phptIni) boolDefault(k string, def bool) bool {
	v, ok := in.raw(k)
	if !ok {
		return def
	}
	return iniBool(v)
}

// parseIniInt разбирает целое (для max_input_vars и т.п.).
func parseIniInt(v string, def int64) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// parseIniBytes разбирает размер с суффиксом K/M/G (как php.ini).
func parseIniBytes(v string, def int64) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	mult := int64(1)
	last := v[len(v)-1]
	switch last {
	case 'k', 'K':
		mult = 1024
		v = v[:len(v)-1]
	case 'm', 'M':
		mult = 1024 * 1024
		v = v[:len(v)-1]
	case 'g', 'G':
		mult = 1024 * 1024 * 1024
		v = v[:len(v)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return def
	}
	return n * mult
}

func (in phptIni) intVal(k string, def int64) int64 {
	v, ok := in.raw(k)
	if !ok {
		return def
	}
	return parseIniInt(v, def)
}

func (in phptIni) sizeVal(k string, def int64) int64 {
	v, ok := in.raw(k)
	if !ok {
		return def
	}
	return parseIniBytes(v, def)
}

// inputConfig — только те настройки, которые влияют на суперглобалы.
type inputConfig struct {
	variablesOrder        string
	requestOrder          string
	registerArgcArgv      bool
	postMaxSize           int64 // 0 = без лимита
	maxInputVars          int64 // 0 = без лимита
	maxNesting            int64 // 0 = без лимита
	fileUploads           bool
	uploadMaxFilesize     int64
	enablePostDataReading bool

	count      int64
	warnedVars bool
}

func parseInputConfig(raw string) inputConfig {
	in := parseIniSection(raw)
	cfg := inputConfig{
		variablesOrder:        in.getString("variables_order", "EGPCS"),
		registerArgcArgv:      in.boolDefault("register_argc_argv", true),
		postMaxSize:           in.sizeVal("post_max_size", 8*1024*1024),
		maxInputVars:          in.intVal("max_input_vars", 1000),
		maxNesting:            in.intVal("max_input_nesting_level", 64),
		fileUploads:           in.boolDefault("file_uploads", true),
		uploadMaxFilesize:     in.sizeVal("upload_max_filesize", 2*1024*1024),
		enablePostDataReading: in.boolDefault("enable_post_data_reading", true),
	}
	if ro, ok := in.raw("request_order"); ok {
		cfg.requestOrder = ro
	} else {
		cfg.requestOrder = filterGPC(cfg.variablesOrder)
	}
	return cfg
}

func (c *inputConfig) hasVar(ch byte) bool {
	// variables_order нечувствителен к регистру: "egpcs" == "EGPCS".
	lo := ch | 0x20
	up := ch &^ 0x20
	return strings.IndexByte(c.variablesOrder, lo) >= 0 || strings.IndexByte(c.variablesOrder, up) >= 0
}

// filterGPC оставляет только G/P/C в порядке вхождения (так php выводит
// request_order из variables_order, если request_order не задан).
func filterGPC(s string) string {
	var b strings.Builder
	for _, ch := range []byte{'G', 'P', 'C'} {
		if strings.IndexByte(s, ch) >= 0 || strings.IndexByte(s, ch|0x20) >= 0 {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// allowVar учитывает max_input_vars: после лимита переменные отбрасываются.
func (c *inputConfig) allowVar() bool {
	if c.maxInputVars <= 0 {
		return true
	}
	if c.count >= c.maxInputVars {
		c.warnedVars = true
		return false
	}
	c.count++
	return true
}

// --- предупреждения стадии request startup ---

func postTooLargeWarning(n, limit int64) string {
	return fmt.Sprintf("\nWarning: POST Content-Length of %d bytes exceeds the limit of %d bytes in Unknown on line 0\n", n, limit)
}

func maxInputVarsWarning(limit int64) string {
	return fmt.Sprintf("\nWarning: Input variables exceeded %d. To increase the limit change max_input_vars in php.ini. in Unknown on line 0\n", limit)
}

func multipartWarning(text string) string {
	return "\nWarning: " + text + " in Unknown on line 0\n"
}

// stripStartup убирает приставку "PHP Request Startup: " (её снимает и
// php-src run-tests.php перед сравнением; в тестах она встречается и с ней,
// и без неё). Применяется к обеим сравниваемым строкам.
func stripStartup(s string) string {
	return strings.ReplaceAll(s, "PHP Request Startup: ", "")
}

// --- разбор query/urlencoded с учётом INI ---

func parseURLInto(root *vm.Array, s string, cfg *inputConfig) {
	if s == "" {
		return
	}
	for _, pair := range strings.Split(s, "&") {
		if pair == "" {
			continue
		}
		k, v := pair, ""
		if i := strings.IndexByte(pair, '='); i >= 0 {
			k, v = pair[:i], pair[i+1:]
		}
		insertParamCfg(root, urlDecode(k), vm.MakeString(urlDecode(v)), cfg)
	}
}

// splitParamKey повторяет разбор имени переменной PHP (как в insertParam):
// base + последовательность сегментов [key] / [] (append). Плоское имя с '['
// без ']' возвращается через isFlat.
func splitParamKey(rawKey string) (base string, segs []paramSeg, flat string, isFlat bool) {
	if strings.Contains(rawKey, "[") && !strings.Contains(rawKey, "]") {
		return "", nil, flatVarName(rawKey), true
	}
	base = rawKey
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
	return base, segs, "", false
}

func unsetTop(root *vm.Array, base string) {
	if i, ok := canonicalInt(base); ok {
		root.Unset(vm.MakeIntKey(i))
		return
	}
	root.Unset(vm.MakeStrKey(base))
}

// insertParamCfg — insertParam + max_input_nesting_level/max_input_vars.
func insertParamCfg(root *vm.Array, rawKey string, val vm.Slot, cfg *inputConfig) {
	base, segs, flat, isFlat := splitParamKey(rawKey)
	if isFlat {
		if !cfg.allowVar() {
			return
		}
		arrSet(root, flat, val)
		return
	}
	base = phpVarName(base)
	if cfg.maxNesting > 0 && int64(len(segs)) > cfg.maxNesting {
		// PHP отбрасывает всю переменную верхнего уровня.
		unsetTop(root, base)
		return
	}
	if !cfg.allowVar() {
		return
	}
	if len(segs) == 0 {
		arrSet(root, base, val)
		return
	}
	setNested(root, base, segs, val)
}

// --- cookie с array-синтаксисом имён ---

func parseCookiesInto(arr *vm.Array, raw string) {
	if raw == "" {
		return
	}
	for _, part := range strings.Split(raw, ";") {
		seg := strings.TrimLeft(part, " \t")
		if seg == "" {
			continue
		}
		name, val := seg, ""
		if i := strings.IndexByte(seg, '='); i >= 0 {
			name, val = seg[:i], seg[i+1:]
		}
		if name == "" {
			continue
		}
		// Имена cookie не percent-decode'ятся, но могут использовать array-
		// синтаксис ("a[b]=1" -> $_COOKIE['a']['b']).
		if strings.Contains(name, "[") && strings.Contains(name, "]") {
			insertParamCfg(arr, name, vm.MakeString(percentDecode(val)), &inputConfig{maxInputVars: 0, maxNesting: 0})
			continue
		}
		key := phpVarName(name)
		if key == "" {
			continue
		}
		if _, ok := arr.Get(vm.MakeStrKey(key)); ok {
			continue // первое вхождение побеждает
		}
		arr.SetStr(key, vm.MakeString(percentDecode(val)))
	}
}

// --- multipart/form-data: $_POST и $_FILES ---

type uploadEntry struct {
	name     string
	fullPath string
	typ      string
	tmp      string
	err      int64
	size     int64
}

const (
	boundaryOK = iota
	boundaryMissing
	boundaryInvalid
)

func parseBoundary(ct string) (string, int) {
	low := strings.ToLower(ct)
	i := strings.Index(low, "boundary=")
	if i < 0 {
		return "", boundaryMissing
	}
	v := strings.TrimLeft(ct[i+len("boundary="):], " \t")
	if v == "" {
		return "", boundaryInvalid
	}
	if v[0] == '"' {
		j := strings.IndexByte(v[1:], '"')
		if j < 0 {
			return "", boundaryInvalid
		}
		b := v[1 : 1+j]
		if b == "" {
			return "", boundaryInvalid
		}
		return b, boundaryOK
	}
	for k := 0; k < len(v); k++ {
		switch v[k] {
		case ';', ',', ' ', '\t':
			if k == 0 {
				return "", boundaryInvalid
			}
			return v[:k], boundaryOK
		}
	}
	return v, boundaryOK
}

func parseUploadBody(ct, body string, cfg *inputConfig) (post, files *vm.Array, warns []string) {
	post, files = vm.NewArray(), vm.NewArray()
	boundary, status := parseBoundary(ct)
	switch status {
	case boundaryMissing:
		return post, files, []string{multipartWarning("Missing boundary in multipart/form-data POST data")}
	case boundaryInvalid:
		return post, files, []string{multipartWarning("Invalid boundary in multipart/form-data POST data")}
	}

	body = strings.ReplaceAll(body, "\r\n", "\n")
	hasClosing := strings.Contains(body, "--"+boundary+"--")
	parts := strings.Split(body, "--"+boundary)
	maxFileSize := int64(-1)

	for i := 1; i < len(parts); i++ {
		seg := parts[i]
		if strings.HasPrefix(seg, "--") {
			continue // закрывающий разделитель
		}
		seg = strings.TrimPrefix(seg, "\n")
		seg = strings.TrimSuffix(seg, "\n")
		hi := strings.Index(seg, "\n\n")
		if hi < 0 {
			continue
		}
		hdr, content := seg[:hi], seg[hi+2:]
		name, filename, ptype, hasName, hasFilename := parsePartHeaders(hdr)

		if !hasName && !hasFilename {
			if strings.Contains(strings.ToLower(hdr), "content-disposition:") {
				warns = append(warns, multipartWarning("File Upload Mime headers garbled"))
			}
			continue
		}

		if hasFilename {
			if !cfg.fileUploads {
				continue
			}
			last := i == len(parts)-1 && !hasClosing
			entry := buildUploadEntry(filename, ptype, content, cfg, maxFileSize, last)
			insertUploadEntry(files, name, entry)
			continue
		}

		// Обычное поле формы.
		if name == "MAX_FILE_SIZE" {
			if n, err := strconv.ParseInt(strings.TrimSpace(content), 10, 64); err == nil {
				maxFileSize = n
			}
		}
		if cfg.hasVar('P') {
			insertParamCfg(post, name, vm.MakeString(content), cfg)
		}
	}
	return post, files, warns
}

func parsePartHeaders(headers string) (name, filename, ptype string, hasName, hasFilename bool) {
	for _, line := range strings.Split(headers, "\n") {
		low := strings.ToLower(line)
		switch {
		case strings.HasPrefix(low, "content-disposition:"):
			name, hasName = dispositionParam(line, "name")
			filename, hasFilename = dispositionParam(line, "filename")
		case strings.HasPrefix(low, "content-type:"):
			ptype = strings.TrimSpace(line[len("content-type:"):])
		}
	}
	return name, filename, ptype, hasName, hasFilename
}

// buildUploadEntry формирует запись $_FILES с учётом INI и MAX_FILE_SIZE.
func buildUploadEntry(filename, ptype, content string, cfg *inputConfig, maxFileSize int64, partial bool) uploadEntry {
	e := uploadEntry{}
	if filename == "" {
		e.err = 4 // UPLOAD_ERR_NO_FILE
		return e
	}
	e.fullPath = filename
	e.name = uploadBaseName(filename)
	size := int64(len(content))
	if cfg.uploadMaxFilesize > 0 && size > cfg.uploadMaxFilesize {
		e.err = 1 // UPLOAD_ERR_INI_SIZE
		return e
	}
	if maxFileSize >= 0 && size > maxFileSize {
		e.err = 2 // UPLOAD_ERR_FORM_SIZE
		return e
	}
	if partial {
		e.err = 3 // UPLOAD_ERR_PARTIAL
		return e
	}
	e.typ = ptype
	e.tmp = "/tmp/phpvm-upload"
	if f, ferr := os.CreateTemp("", "phpvm-upload"); ferr == nil {
		f.WriteString(content)
		e.tmp = f.Name()
		f.Close()
	}
	e.size = size
	return e
}

// uploadBaseName повторяет php_basename для имён файлов ('/' и '\').
func uploadBaseName(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' || s[i] == '\\' {
			return s[i+1:]
		}
	}
	return s
}

// parseFileField строго разбирает имя поля файла: base + [key]/[] сегменты.
// Malformed-имена (foo[]bar, file[[type], file[name][) отбрасываются целиком
// (Bug #55500 / rfc1867_malicious_input). anonymous == true для поля без имени.
func parseFileField(raw string) (base string, segs []paramSeg, anonymous, ok bool) {
	if raw == "" {
		return "", nil, true, true
	}
	if !strings.Contains(raw, "[") {
		if strings.Contains(raw, "]") {
			return "", nil, false, false
		}
		return phpVarName(raw), nil, false, true
	}
	i := strings.IndexByte(raw, '[')
	base = phpVarName(raw[:i])
	rest := raw[i:]
	for len(rest) > 0 {
		if rest[0] != '[' {
			return "", nil, false, false
		}
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			return "", nil, false, false
		}
		c := rest[1:j]
		if strings.ContainsAny(c, "[]") {
			return "", nil, false, false
		}
		if c == "" {
			segs = append(segs, paramSeg{append: true})
		} else {
			segs = append(segs, paramSeg{key: c})
		}
		rest = rest[j+1:]
	}
	return base, segs, false, true
}

func insertUploadEntry(files *vm.Array, fieldName string, e uploadEntry) {
	attrs := [...]struct {
		key string
		val vm.Slot
	}{
		{"name", vm.MakeString(e.name)},
		{"full_path", vm.MakeString(e.fullPath)},
		{"type", vm.MakeString(e.typ)},
		{"tmp_name", vm.MakeString(e.tmp)},
		{"error", vm.MakeInt(e.err)},
		{"size", vm.MakeInt(e.size)},
	}

	base, segs, anonymous, ok := parseFileField(fieldName)
	if !ok {
		return // повреждённое имя — как в PHP, файл отбрасывается
	}
	if anonymous {
		entry := vm.NewArray()
		for _, a := range attrs {
			entry.SetStr(a.key, a.val)
		}
		_, _ = files.Append(vm.MakeArray(entry))
		return
	}
	entry := childArray(files, base)
	for _, a := range attrs {
		if len(segs) == 0 {
			arrSet(entry, a.key, a.val)
		} else {
			setNested(entry, a.key, segs, a.val)
		}
	}
}

// dispositionParam извлекает параметр Content-Disposition и сообщает, был ли
// он задан (в отличие от пустого значения).
func dispositionParam(line, key string) (string, bool) {
	for i := 0; i+len(key)+1 <= len(line); i++ {
		if line[i:i+len(key)] != key || line[i+len(key)] != '=' {
			continue
		}
		if i > 0 && line[i-1] != ';' && line[i-1] != ' ' && line[i-1] != '\t' {
			continue
		}
		return parseHTTPToken(line[i+len(key)+1:]), true
	}
	return "", false
}

// parseHTTPToken разбирает значение параметра RFC1867 с поддержкой кавычек.
func parseHTTPToken(v string) string {
	v = strings.TrimLeft(v, " \t")
	if v == "" {
		return ""
	}
	if v[0] == '"' {
		return unquoteToken(v, '"', '"')
	}
	if v[0] == '\'' {
		return unquoteToken(v, '\'', '\'')
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\\' && i+1 < len(v) && v[i+1] == '\\' {
			b.WriteByte('\\')
			i++
			continue
		}
		if c == ';' || c == ' ' || c == '\t' {
			break
		}
		b.WriteByte(c)
	}
	return b.String()
}

func unquoteToken(v string, q, esc byte) string {
	var b strings.Builder
	for i := 1; i < len(v); i++ {
		c := v[i]
		if c == '\\' && i+1 < len(v) && (v[i+1] == '\\' || v[i+1] == esc) {
			b.WriteByte(v[i+1])
			i++
			continue
		}
		if c == q {
			break
		}
		b.WriteByte(c)
	}
	return b.String()
}

// --- HTTP-заголовки --POST_RAW-- ---

var httpHeaderRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*:`)

// parseRawHeaders разбирает заголовки --POST_RAW-- в map (ключ как в исходнике).
func parseRawHeaders(raw string) map[string]string {
	out := map[string]string{}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	for _, line := range strings.Split(raw, "\n") {
		if !httpHeaderRe.MatchString(line) {
			break
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			break
		}
		out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
	}
	return out
}

// --- собственные проверки (сверено с php 8.3) ---

func multipartPart(disp, ctype, content string) string {
	s := "Content-Disposition: " + disp + "\n"
	if ctype != "" {
		s += "Content-Type: " + ctype + "\n"
	}
	return s + "\n" + content + "\n"
}

func multipartBody(boundary string, parts ...string) string {
	var b strings.Builder
	b.WriteString("Content-Type: multipart/form-data; boundary=" + boundary + "\n")
	for _, p := range parts {
		b.WriteString("--" + boundary + "\n")
		b.WriteString(p)
	}
	b.WriteString("--" + boundary + "--\n")
	return b.String()
}

func TestSuperglobalsFilesArray(t *testing.T) {
	raw := multipartBody("phpvmB",
		multipartPart(`form-data; name="file[]"; filename="file1.txt"`, "text/plain-file1", "1"),
		multipartPart(`form-data; name="file[2]"; filename="file2.txt"`, "text/plain-file2", "22"),
		multipartPart(`form-data; name="file[]"; filename="file3.txt"`, "text/plain-file3", "333"),
	)
	p := parsePhpt("--POST_RAW--\n" + raw +
		"--FILE--\n<?php echo implode(',', array_keys($_FILES['file']['name'])),'|'," +
		"$_FILES['file']['name'][0],',',$_FILES['file']['name'][2],',',$_FILES['file']['name'][3],'|'," +
		"$_FILES['file']['size'][0],$_FILES['file']['size'][2],$_FILES['file']['size'][3];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "0,2,3|file1.txt,file2.txt,file3.txt|123"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsFilesDeep(t *testing.T) {
	raw := multipartBody("phpvmB",
		multipartPart(`form-data; name="a[b][]"; filename="f1.txt"`, "text/plain", "1"),
		multipartPart(`form-data; name="a[b][]"; filename="f2.txt"`, "text/plain", "2"),
	)
	p := parsePhpt("--POST_RAW--\n" + raw +
		"--FILE--\n<?php echo $_FILES['a']['name']['b'][0],$_FILES['a']['name']['b'][1],'|'," +
		"$_FILES['a']['type']['b'][1],'|',$_FILES['a']['size']['b'][0];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "f1.txtf2.txt|text/plain|1"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsFilesAnonymousAndPaths(t *testing.T) {
	raw := multipartBody("phpvmB",
		multipartPart(`form-data; filename="file1.txt"`, "text/plain-a", "1"),
		multipartPart(`form-data; name="files[]"; filename="directory/sub/file2.txt"`, "text/plain", "2"),
	)
	p := parsePhpt("--POST_RAW--\n" + raw +
		"--FILE--\n<?php echo $_FILES[0]['name'],'|',$_FILES['files']['name'][0],'|'," +
		"$_FILES['files']['full_path'][0],'|',$_FILES['files']['size'][0];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "file1.txt|file2.txt|directory/sub/file2.txt|1"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsFileUploadsDisabled(t *testing.T) {
	raw := multipartBody("phpvmB",
		multipartPart(`form-data; name="foo"`, "", "bar"),
		multipartPart(`form-data; name="file1"; filename="file1.txt"`, "text/plain", "1"),
		multipartPart(`form-data; name="bar"`, "", "foo"),
	)
	p := parsePhpt("--INI--\nfile_uploads=0\n--POST_RAW--\n" + raw +
		"--FILE--\n<?php echo count($_FILES),'|',$_POST['foo'],$_POST['bar'];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "0|barfoo"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsFileCorruptedNames(t *testing.T) {
	// Bug #55500 + rfc1867_malicious_input: повреждённые индексы отбрасываются.
	raw := multipartBody("phpvmB",
		multipartPart(`form-data; name="file[]"; filename="file1.txt"`, "text/plain", "1"),
		multipartPart(`form-data; name="file[[type]"; filename="file2.txt"`, "text/plain", "2"),
		multipartPart(`form-data; name="file[[name]"; filename="file3.txt"`, "text/plain", "3"),
		multipartPart(`form-data; name="file[name]["; filename="file4.txt"`, "text/plain", "4"),
		multipartPart(`form-data; name="foo[]bar"; filename="file5.txt"`, "text/plain", "5"),
	)
	p := parsePhpt("--POST_RAW--\n" + raw +
		"--FILE--\n<?php echo count($_FILES),'|',count($_FILES['file']['name']),'|',$_FILES['file']['name'][0];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "1|1|file1.txt"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsFilePartial(t *testing.T) {
	// rfc1867_missing_boundary_2: тело без закрывающего boundary -> error 3.
	raw := "Content-Type: multipart/form-data; boundary=phpvmB\n" +
		"--phpvmB\n" +
		"Content-Disposition: form-data; name=\"file1\"; filename=\"file1.txt\"\n" +
		"Content-Type: text/plain-file1\n\n1\n"
	p := parsePhpt("--INI--\nfile_uploads=1\n--POST_RAW--\n" + raw +
		"--FILE--\n<?php echo $_FILES['file1']['error'],'|',$_FILES['file1']['name'],'|'," +
		"$_FILES['file1']['type'],'|',$_FILES['file1']['tmp_name'],'|',$_FILES['file1']['size'];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "3|file1.txt|||0"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsBoundaryWarnings(t *testing.T) {
	missing := parsePhpt("--POST_RAW--\nContent-Type: multipart/form-data\n--phpvmB\nContent-Disposition: form-data; name=\"foobar\"\n\n1\n--phpvmB--\n--FILE--\n<?php var_dump($_FILES);var_dump($_POST);")
	got, err := runPhptAt(missing, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "Missing boundary in multipart/form-data POST data") {
		t.Fatalf("missing boundary warning not emitted: %q", got)
	}
	invalid := parsePhpt("--POST_RAW--\nContent-Type: multipart/form-data; boundary=\"foobar\n--phpvmB\nContent-Disposition: form-data; name=\"foobar\"\n\n1\n--phpvmB--\n--FILE--\n<?php var_dump($_FILES);var_dump($_POST);")
	got, err = runPhptAt(invalid, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "Invalid boundary in multipart/form-data POST data") {
		t.Fatalf("invalid boundary warning not emitted: %q", got)
	}
	garbled := parsePhpt("--POST_RAW--\nContent-Type: multipart/form-data; boundary=phpvmB\n--phpvmB\nContent-Disposition: form-data\n\n\n--phpvmB--\n--FILE--\n<?php var_dump($_FILES);var_dump($_POST);")
	got, err = runPhptAt(garbled, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "File Upload Mime headers garbled") {
		t.Fatalf("garbled warning not emitted: %q", got)
	}
}

func TestSuperglobalsRequestOrder(t *testing.T) {
	// request_order по умолчанию = производная variables_order (GPC),
	// поэтому cookie тоже участвует, но перекрывается более поздними.
	p := parsePhpt("--GET--\ng=1&shared=G\n--POST--\np=2&shared=P\n--COOKIE--\nc=3; shared=C\n--FILE--\n<?php echo json_encode($_REQUEST);")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := `{"g":"1","shared":"C","p":"2","c":"3"}`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	// request_order=GP отбрасывает cookie.
	p = parsePhpt("--INI--\nrequest_order=GP\n--COOKIE--\nonly=C\n--GET--\ng=1\n--FILE--\n<?php echo json_encode($_REQUEST);")
	got, err = runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := `{"g":"1"}`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsCookiesArray(t *testing.T) {
	p := parsePhpt("--COOKIE--\na[b]=1; d[]=3; c%5B%5D=2; dup=x; dup=y\n--FILE--\n<?php echo $_COOKIE['a']['b'],'|',$_COOKIE['d'][0],'|',$_COOKIE['c%5B%5D'],'|',$_COOKIE['dup'];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "1|3|2|x"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsServerHeaders(t *testing.T) {
	raw := multipartBody("phpvmB", multipartPart(`form-data; name="data"`, "", "abc"))
	p := parsePhpt("--GET--\nq=1\n--POST_RAW--\n" + raw +
		"--FILE--\n<?php echo $_SERVER['REQUEST_METHOD'],'|',$_SERVER['CONTENT_TYPE'],'|'," +
		"($_SERVER['CONTENT_LENGTH']>0),'|',$_SERVER['QUERY_STRING'];")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "POST|multipart/form-data; boundary=phpvmB|1|q=1"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSuperglobalsVariablesOrder(t *testing.T) {
	// variables_order=GPC => $_SERVER/$_ENV пусты (Bug #29971).
	p := parsePhpt("--INI--\nvariables_order=GPC\n--ENV--\nFOO=bar\n--GET--\na=1\n--FILE--\n<?php var_dump($_ENV, $_SERVER, $_GET['a']);")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "array(0) {\n}\narray(0) {\n}\nstring(1) \"1\"") {
		t.Fatalf("variables_order not honored: %q", got)
	}
}

func TestSuperglobalsPostMaxSize(t *testing.T) {
	p := parsePhpt("--INI--\npost_max_size=1\n--POST--\nfoobar=1\n--FILE--\n<?php var_dump($_FILES, $_POST);")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "POST Content-Length of 8 bytes exceeds the limit of 1 bytes") {
		t.Fatalf("post_max_size warning missing: %q", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "array(0) {\n}\narray(0) {\n}") {
		t.Fatalf("post not discarded: %q", got)
	}
}

func TestSuperglobalsNestingLimit(t *testing.T) {
	// Bug #61000: при превышении max_input_nesting_level переменная отбрасывается.
	p := parsePhpt("--INI--\nmax_input_nesting_level=2\n--POST--\n1[a][]=foo&1[a][b][c]=bar\n--GET--\na[a][]=foo&a[a][b][c]=bar\n--FILE--\n<?php print_r($_GET);print_r($_POST);")
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(got, "foo") || strings.Contains(got, "bar") {
		t.Fatalf("nesting limit not applied: %q", got)
	}
}

// TestSuperglobalsGetPHPDiff сверяет разбор $_GET с живым php (parse_str).
func TestSuperglobalsGetPHPDiff(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	queries := []string{
		"a=1&b[]=2&b[]=3&c[x]=y",
		"a[]=1&a[0]=5",
		"a[a[]]=1&a[b[]]=3",
		"a[]]=3&a[[]=4",
		"x=1&x=2",
		"a=hello+world&d=%20e",
		"123[]=SEGV",
		"k[a][b][c]=v&k[a][d]=w",
		"empty=&flag",
	}
	for _, q := range queries {
		p := parsePhpt("--GET--\n" + q + "\n--FILE--\n<?php print_r($_GET);")
		got, err := runPhptAt(p, "test.phpt")
		if err != nil {
			t.Fatalf("run %q: %v", q, err)
		}
		cmd := exec.Command(phppath, "-n", "-r", `parse_str(getenv("PHPVM_Q"), $a); print_r($a);`)
		cmd.Env = append(cmd.Environ(), "PHPVM_Q="+q)
		wantb, err := cmd.Output()
		if err != nil {
			t.Fatalf("php parse_str %q: %v", q, err)
		}
		if strings.TrimSpace(got) != strings.TrimSpace(string(wantb)) {
			t.Errorf("query %q:\n vm  = %q\n php = %q", q, got, string(wantb))
		}
	}
}

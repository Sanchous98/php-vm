package php

// builtins_browscap.go — get_browser(): разбор browscap.ini и сопоставление
// User-Agent по правилам Zend (ext/standard/browscap.c): префикс без
// placeholder'ов, до пяти "contains"-подстрок, regex из шаблона, выбор самого
// специфичного совпадения, наследование свойств по Parent.

import (
	"os"
	"regexp"
	"strings"

	"phpvm/internal/vm"
)

const browscapNumContains = 5

type browscapKV struct{ key, value string }

type browscapEntry struct {
	pattern string
	parent  string
	kvStart int
	kvEnd   int
	prefix  int
	cStart  [browscapNumContains]int
	cLen    [browscapNumContains]int
}

func (e *browscapEntry) minLen() int {
	n := e.prefix
	for i := 0; i < browscapNumContains; i++ {
		n += e.cLen[i]
	}
	return n
}

type browscapData struct {
	order  []*browscapEntry
	byName map[string]*browscapEntry
	kv     []browscapKV
}

var browscapCache = map[string]*browscapData{}
var browscapFailed = map[string]bool{}
var browscapRegexCache = map[string]*regexp.Regexp{}

func browscapIsPlaceholder(c byte) bool { return c == '?' || c == '*' }

func browscapComputePrefix(pattern string) int {
	i := 0
	for ; i < len(pattern); i++ {
		if browscapIsPlaceholder(pattern[i]) {
			break
		}
	}
	if i > 255 {
		i = 255
	}
	return i
}

// browscapComputeContains повторяет одноимённую функцию Zend: ищет первую
// "длинную" (>=2 символа) подстроку без placeholder'ов начиная с pos.
func browscapComputeContains(pattern string, pos int, start, ln *int) int {
	i := pos
	for ; i < len(pattern); i++ {
		if !browscapIsPlaceholder(pattern[i]) {
			if i+1 < len(pattern) && !browscapIsPlaceholder(pattern[i+1]) {
				break
			}
		}
	}
	*start = i
	for ; i < len(pattern); i++ {
		if browscapIsPlaceholder(pattern[i]) {
			break
		}
	}
	n := i - *start
	if n > 255 {
		n = 255
	}
	*ln = n
	return i
}

// browscapConvertPattern повторяет browscap_convert_pattern: шаблон в нижнем
// регистре, `?` → `.`, `*` → `.*`, спецсимволы PCRE экранируются.
func browscapConvertPattern(pattern string) string {
	var b strings.Builder
	b.Grow(len(pattern) + 16)
	b.WriteString("~^")
	lc := strings.ToLower(pattern)
	for i := 0; i < len(lc); i++ {
		switch lc[i] {
		case '?':
			b.WriteByte('.')
		case '*':
			b.WriteString(".*")
		case '.':
			b.WriteString(`\.`)
		case '\\':
			b.WriteString(`\\`)
		case '(':
			b.WriteString(`\(`)
		case ')':
			b.WriteString(`\)`)
		case '~':
			b.WriteString(`\~`)
		case '+':
			b.WriteString(`\+`)
		default:
			b.WriteByte(lc[i])
		}
	}
	b.WriteString("$~")
	return b.String()
}

func browscapRegex(pattern string) *regexp.Regexp {
	if re, ok := browscapRegexCache[pattern]; ok {
		return re
	}
	// PCRE-строка с делимитерами `~^...$~`; Go-regexp делимитеров не знает,
	// компилируем внутреннюю часть (сама строка с `~` идёт в browser_name_regex).
	conv := browscapConvertPattern(pattern)
	inner := strings.TrimSuffix(strings.TrimPrefix(conv, "~"), "~")
	re, err := regexp.Compile(inner)
	if err != nil {
		re = nil
	}
	browscapRegexCache[pattern] = re
	return re
}

// unquoteBrowscapValue снимает обрамляющие кавычки (INI RAW-сканер).
func unquoteBrowscapValue(v string) string {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}

// parseBrowscap разбирает browscap.ini: секции — шаблоны, ключи — свойства
// (в нижнем регистре), значения "on/yes/true" → "1", "no/off/none/false" → "".
func parseBrowscap(src string) *browscapData {
	d := &browscapData{byName: map[string]*browscapEntry{}}
	cur := (*browscapEntry)(nil)
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		if s == "" || s[0] == ';' || s[0] == '#' {
			continue
		}
		if s[0] == '[' {
			end := strings.LastIndexByte(s, ']')
			if end < 0 {
				continue
			}
			name := s[1:end]
			e := &browscapEntry{pattern: name, kvStart: len(d.kv)}
			pos := browscapComputePrefix(name)
			e.prefix = pos
			for i := 0; i < browscapNumContains; i++ {
				pos = browscapComputeContains(name, pos, &e.cStart[i], &e.cLen[i])
			}
			d.order = append(d.order, e)
			d.byName[name] = e
			cur = e
			continue
		}
		eq := strings.IndexByte(s, '=')
		if eq < 0 || cur == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		if key == "" {
			continue
		}
		val := unquoteBrowscapValue(strings.TrimSpace(s[eq+1:]))
		switch strings.ToLower(val) {
		case "on", "yes", "true":
			val = "1"
		case "no", "off", "none", "false":
			val = ""
		}
		if key == "parent" {
			if strings.EqualFold(cur.pattern, val) {
				// Zend: E_CORE_ERROR; в тестах не встречается — просто игнорируем.
				continue
			}
			cur.parent = val
			continue
		}
		d.kv = append(d.kv, browscapKV{key, val})
		cur.kvEnd = len(d.kv)
	}
	return d
}

// browscapLoad читает и кэширует файл; предупреждение Zend при ошибке чтения.
func browscapLoad(ctx *vm.Context, path string) *browscapData {
	if d, ok := browscapCache[path]; ok {
		return d
	}
	if browscapFailed[path] {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		ctx.VM().Warning("Cannot open \"" + path + "\" for reading")
		browscapFailed[path] = true
		return nil
	}
	d := parseBrowscap(string(data))
	browscapCache[path] = d
	return d
}

// browscapNonPlaceholders считает символы шаблона, не являющиеся `?`/`*`.
func browscapNonPlaceholders(pattern string) int {
	n := 0
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '?' && pattern[i] != '*' {
			n++
		}
	}
	return n
}

// browscapCompare повторяет browser_reg_compare: быстрые проверки, точное
// совпадение, затем regex; среди совпадений выбирается самый специфичный.
func browscapCompare(e *browscapEntry, agent string, found **browscapEntry) bool {
	if len(agent) < e.minLen() {
		return false
	}
	if e.prefix > len(e.pattern) || e.prefix > len(agent) {
		return false
	}
	if !strings.EqualFold(agent[:e.prefix], e.pattern[:e.prefix]) {
		return false
	}
	patLower := strings.ToLower(e.pattern)
	cur := e.prefix
	for i := 0; i < browscapNumContains; i++ {
		if e.cLen[i] != 0 {
			seg := patLower[e.cStart[i] : e.cStart[i]+e.cLen[i]]
			idx := strings.Index(agent[cur:], seg)
			if idx < 0 {
				return false
			}
			cur += idx + e.cLen[i]
		}
	}
	if agent == patLower {
		*found = e
		return true
	}
	if re := browscapRegex(e.pattern); re != nil && re.MatchString(agent) {
		if *found != nil {
			if browscapNonPlaceholders((*found).pattern) < browscapNonPlaceholders(e.pattern) {
				*found = e
			}
		} else {
			*found = e
		}
	}
	return false
}

// browscapEntryToArray собирает массив секции (regex, pattern, parent, свойства).
func browscapEntryToArray(d *browscapData, e *browscapEntry) *vm.Array {
	arr := vm.NewArray()
	_ = arr.SetStr("browser_name_regex", vm.MakeString(browscapConvertPattern(e.pattern)))
	_ = arr.SetStr("browser_name_pattern", vm.MakeString(e.pattern))
	if e.parent != "" {
		_ = arr.SetStr("parent", vm.MakeString(e.parent))
	}
	for i := e.kvStart; i < e.kvEnd; i++ {
		kv := d.kv[i]
		if _, ok := arr.Get(vm.MakeStrKey(kv.key)); !ok {
			_ = arr.SetStr(kv.key, vm.MakeString(kv.value))
		}
	}
	return arr
}

// getBrowserFn реализует get_browser(agent_name, return_array).
func getBrowserFn(ctx *vm.Context) vm.Value {
	path := ""
	if v, ok := iniValues["browscap"]; ok {
		path = v
	}
	if path == "" {
		ctx.VM().Warning("get_browser(): browscap ini directive not set")
		return vm.Bool(false)
	}
	bdata := browscapLoad(ctx, path)
	if bdata == nil {
		return vm.Bool(false)
	}
	args := ctx.Args()
	var agent string
	if len(args) > 0 && args[0].Type() != vm.NullType {
		agent = args[0].ToString()
	} else {
		srv, ok := ctx.VM().GlobalRef("$_SERVER")
		var v vm.Slot
		if ok {
			v = vm.Deref(srv)
		}
		uaFound := false
		if v.Type() == vm.ArrayType {
			if ua, found := v.AsArray().Get(vm.MakeStrKey("HTTP_USER_AGENT")); found {
				agent = vm.Deref(ua).ToString()
				uaFound = true
			}
		}
		if !uaFound {
			ctx.VM().Warning("get_browser(): HTTP_USER_AGENT variable is not set, cannot determine user agent name")
			return vm.Bool(false)
		}
	}
	returnArray := false
	if len(args) > 1 {
		returnArray = args[1].ToBool()
	}
	agentLower := strings.ToLower(agent)
	found := bdata.byName[agentLower]
	if found == nil {
		for _, e := range bdata.order {
			if browscapCompare(e, agentLower, &found) {
				break
			}
		}
	}
	if found == nil {
		found = bdata.byName["Default Browser Capability Settings"]
	}
	if found == nil {
		return vm.Bool(false)
	}
	arr := browscapEntryToArray(bdata, found)
	for cur := found; cur.parent != ""; {
		p := bdata.byName[cur.parent]
		if p == nil {
			break
		}
		parr := browscapEntryToArray(bdata, p)
		for k, v := range parr.All() {
			if _, ok := arr.Get(k); !ok {
				_ = arr.Set(k, v)
			}
		}
		cur = p
	}
	if returnArray {
		return vm.MakeArray(arr)
	}
	order := make([]string, 0, arr.Len())
	props := map[string]vm.Value{}
	for k, v := range arr.All() {
		key := ""
		if k.IsStr() {
			key = k.StrVal()
		} else {
			key = k.PHPString()
		}
		order = append(order, key)
		props[key] = slotFromValue(v)
	}
	return slotFromValue(makeStdClass(ctx.VM(), order, props))
}

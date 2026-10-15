package php

import (
	"encoding/json/jsontext"
	"errors"
	"io"
	"math"
	"phpvm/internal/vm"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Файл содержит реализацию json_encode/json_decode.

// ============================================================================
// dateAgent: дата/время, JSON, URL и прочие мелкие встроенные функции.
// ============================================================================

const (
	jsonHexTag                   = 1
	jsonHexAmp                   = 2
	jsonHexApos                  = 4
	jsonHexQuot                  = 8
	jsonForceObject              = 16
	jsonNumericCheck             = 32
	jsonUnescapedSlashes         = 64
	jsonPrettyPrint              = 128
	jsonUnescapedUnicode         = 256
	jsonPartialOutputOnError     = 512
	jsonPreserveZeroFraction     = 1024
	jsonUnescapedLineTerminators = 2048
	jsonInvalidUTF8Ignore        = 1048576
	jsonInvalidUTF8Substitute    = 2097152
	jsonObjectAsArray            = 1
	jsonBigintAsString           = 2
	jsonThrowOnError             = 4194304

	jsonErrorNone            = 0
	jsonErrorDepth           = 1
	jsonErrorStateMismatch   = 2
	jsonErrorCtrlChar        = 3
	jsonErrorSyntax          = 4
	jsonErrorUTF8            = 5
	jsonErrorRecursion       = 6
	jsonErrorInfOrNan        = 7
	jsonErrorUnsupportedType = 8
	jsonErrorInvalidProperty = 9
	jsonErrorUTF16           = 10
	jsonErrorNonBackedEnum   = 11

	// stdClassIndex — индекс stdClass в vm.classes (порядок NewExceptionClasses
	// фиксирован: 11 предопределённых классов, stdClass последний).
	stdClassIndex = 11
)

func jsonLastErrorMsg(code int) string {
	switch code {
	case jsonErrorNone:
		return "No error"
	case jsonErrorDepth:
		return "Maximum stack depth exceeded"
	case jsonErrorStateMismatch:
		return "State mismatch (invalid or malformed JSON)"
	case jsonErrorCtrlChar:
		return "Control character error, possibly incorrectly encoded"
	case jsonErrorSyntax:
		return "Syntax error"
	case jsonErrorUTF8:
		return "Malformed UTF-8 characters, possibly incorrectly encoded"
	case jsonErrorRecursion:
		return "Recursion detected"
	case jsonErrorInfOrNan:
		return "Inf and NaN cannot be JSON encoded"
	case jsonErrorUnsupportedType:
		return "Type is not supported"
	case jsonErrorInvalidProperty:
		return "The decoded property name is invalid"
	case jsonErrorUTF16:
		return "Single unpaired UTF-16 surrogate in unicode escape"
	case jsonErrorNonBackedEnum:
		return "Non-backed enums have no default serialization"
	}
	return "Unknown error"
}

// --- JSON ENCODE ---

type jsonEncoder struct {
	vm       *vm.VM
	ctx      *vm.Context
	buf      strings.Builder
	flags    int64
	maxDepth int
	pretty   bool
	err      int
	qbuf     []byte // скретч для jsontext.AppendQuote — без аллокаций на строку
}

type jsonEntry struct {
	key string
	val vm.Slot
}

func jsonEncodeFn(ctx *vm.Context) vm.Value {
	flags := int64(0)
	if len(ctx.Args()) >= 2 && !arg(ctx, 1).IsNull() {
		flags = arg(ctx, 1).ToInt()
	}
	depth := 512
	if len(ctx.Args()) >= 3 && !arg(ctx, 2).IsNull() {
		depth = int(arg(ctx, 2).ToInt())
	}
	e := &jsonEncoder{vm: ctx.VM(), ctx: ctx, flags: flags, maxDepth: depth, pretty: flags&jsonPrettyPrint != 0}
	if !e.encode(arg0(ctx)) {
		jsonLastError = e.err
		if flags&jsonThrowOnError != 0 {
			ctx.Throw(vm.NewThrowable("JsonException", jsonLastErrorMsg(e.err)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return vm.Bool(false)
	}
	jsonLastError = 0
	return vm.Str(e.buf.String())
}

// objImplementsJsonSerializable сообщает, реализует ли класс JsonSerializable.
func objImplementsJsonSerializable(ci vm.ClassInfo) bool {
	if ci == nil {
		return false
	}
	for _, n := range ci.InterfaceNames() {
		if n == "JsonSerializable" {
			return true
		}
	}
	return false
}

func (e *jsonEncoder) writeIndent(n int) {
	for i := 0; i < n; i++ {
		e.buf.WriteString("    ")
	}
}

func (e *jsonEncoder) encode(v vm.Slot) bool {
	return e.encodeDepth(v, 0)
}

func (e *jsonEncoder) encodeDepth(v vm.Slot, depth int) bool {
	if depth > e.maxDepth {
		e.err = jsonErrorDepth
		return false
	}
	switch v.Type() {
	case vm.NullType:
		e.buf.WriteString("null")
	case vm.BoolType:
		if v.ToBool() {
			e.buf.WriteString("true")
		} else {
			e.buf.WriteString("false")
		}
	case vm.IntType:
		e.buf.WriteString(strconv.FormatInt(v.ToInt(), 10))
	case vm.FloatType:
		return e.encodeFloat(v.ToFloat())
	case vm.StringType:
		if e.flags&jsonNumericCheck != 0 {
			if s := v.StringVal(); s != "" && vm.IsNumericString(s) {
				return e.encodeNumericString(s)
			}
		}
		return e.encodeString(v.StringVal())
	case vm.ArrayType:
		return e.encodeArray(v.AsArray(), depth)
	case vm.ObjectType:
		o := e.vm.SlotObject(v)
		if o != nil {
			if ci := o.Class(); ci != nil {
				if ci.IsEnum() && !ci.HasMethod("jsonSerialize") {
					if ci.EnumBacking() == "" {
						e.err = jsonErrorNonBackedEnum
						return false
					}
					if val, ok := o.GetProp("value"); ok {
						return e.encodeDepth(slotFromValue(val), depth)
					}
					return true
				}
				if ci.HasMethod("jsonSerialize") && objImplementsJsonSerializable(ci) {
					var res vm.Slot
					var cerr error
					e.vm.WithSerializeLock(func() {
						res, cerr = ci.Call(o, e.ctx, "jsonSerialize", nil)
					})
					if cerr != nil {
						rethrowCall(e.ctx, cerr)
					}
					return e.encodeDepth(res, depth)
				}
			}
		}
		ents := objectJSONEntries(e.vm, o)
		return e.encodeEntries(ents, depth)
	default:
		e.err = jsonErrorUnsupportedType
		return false
	}
	return true
}

func (e *jsonEncoder) encodeFloat(f float64) bool {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		e.err = jsonErrorInfOrNan
		return false
	}
	s := phpFormatFloat(f, e.flags&jsonPreserveZeroFraction != 0)
	s = strings.Replace(s, "E", "e", 1)
	e.buf.WriteString(s)
	return true
}

func (e *jsonEncoder) encodeNumericString(s string) bool {
	if strings.ContainsAny(s, ".eE") {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return e.encodeFloat(f)
		}
	} else {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			e.buf.WriteString(strconv.FormatInt(n, 10))
			return true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return e.encodeFloat(f)
		}
	}
	return e.encodeString(s)
}

func (e *jsonEncoder) encodeArray(a *vm.Array, depth int) bool {
	list := a.Len() == 0
	if a.Len() > 0 {
		list = true
		expect := int64(0)
		for k := range a.All() {
			if k.IsStr() || k.IntVal() != expect {
				list = false
				break
			}
			expect++
		}
	}
	if e.flags&jsonForceObject != 0 {
		list = false
	}
	if list {
		e.buf.WriteByte('[')
		first := true
		for _, v := range a.Values() {
			if !first {
				e.buf.WriteByte(',')
			}
			first = false
			if e.pretty {
				e.buf.WriteByte('\n')
				e.writeIndent(depth + 1)
			}
			if !e.encodeDepth(v, depth+1) {
				return false
			}
		}
		if e.pretty && !first {
			e.buf.WriteByte('\n')
			e.writeIndent(depth)
		}
		e.buf.WriteByte(']')
		return true
	}
	ents := make([]jsonEntry, 0, a.Len())
	for k, v := range a.All() {
		ents = append(ents, jsonEntry{k.PHPString(), v})
	}
	return e.encodeEntries(ents, depth)
}

func objectJSONEntries(m *vm.VM, o *vm.Object) []jsonEntry {
	props := m.ObjectPropMap(o)
	if len(props) == 0 {
		if len(objKeyOrder[o]) == 0 {
			return nil
		}
	}
	keys := objKeyOrder[o]
	if keys == nil {
		keys = make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	ents := make([]jsonEntry, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		if v, ok := props[k]; ok {
			ents = append(ents, jsonEntry{k, slotFromValue(v)})
			seen[k] = true
		}
	}
	if len(ents) != len(props) {
		extra := make([]string, 0)
		for k := range props {
			if !seen[k] {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra)
		for _, k := range extra {
			ents = append(ents, jsonEntry{k, slotFromValue(props[k])})
		}
	}
	return ents
}

func (e *jsonEncoder) encodeEntries(ents []jsonEntry, depth int) bool {
	e.buf.WriteByte('{')
	for i, en := range ents {
		if i > 0 {
			e.buf.WriteByte(',')
		}
		if e.pretty {
			e.buf.WriteByte('\n')
			e.writeIndent(depth + 1)
		}
		if !e.encodeString(en.key) {
			return false
		}
		e.buf.WriteByte(':')
		if e.pretty {
			e.buf.WriteByte(' ')
		}
		if !e.encodeDepth(en.val, depth+1) {
			return false
		}
	}
	if e.pretty && len(ents) > 0 {
		e.buf.WriteByte('\n')
		e.writeIndent(depth)
	}
	e.buf.WriteByte('}')
	return true
}

func writeU4(b *strings.Builder, r rune) {
	b.WriteString("\\u")
	b.WriteByte(hexLower[(r>>12)&0xF])
	b.WriteByte(hexLower[(r>>8)&0xF])
	b.WriteByte(hexLower[(r>>4)&0xF])
	b.WriteByte(hexLower[r&0xF])
}

func (e *jsonEncoder) encodeString(s string) bool {
	// Невалидный UTF-8 — пре-проходом (IGNORE выбрасывает, SUBSTITUTE → U+FFFD),
	// чтобы jsontext всегда видел корректный UTF-8 (как PHP).
	if e.flags&jsonInvalidUTF8Ignore != 0 {
		s = sanitizeInvalidUTF8Raw(s, false)
	} else if e.flags&jsonInvalidUTF8Substitute != 0 {
		s = sanitizeInvalidUTF8Raw(s, true)
	}
	// Быстрый путь: чистый ASCII без требующих эскейпа символов — как есть.
	if jsonPlainString(s, e.flags) {
		e.buf.WriteByte('"')
		e.buf.WriteString(s)
		e.buf.WriteByte('"')
		return true
	}
	q, err := jsontext.AppendQuote(e.qbuf[:0], s)
	e.qbuf = q
	if err != nil {
		e.err = jsonErrorUTF8
		return false
	}
	e.writePHPQuoted(q)
	return true
}

// jsonPlainString сообщает, что строку можно записать в JSON как есть:
// ASCII без кавычек/бэкслеша/слэша/control и без символов, которых требуют
// HEX-флаги. Всё остальное (не-ASCII, спецсимволы) идёт путём jsontext.
func jsonPlainString(s string, flags int64) bool {
	tag := flags&jsonHexTag != 0
	amp := flags&jsonHexAmp != 0
	apos := flags&jsonHexApos != 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 || c < 0x20 || c == '"' || c == '\\' || c == '/' {
			return false
		}
		if tag && (c == '<' || c == '>') {
			return false
		}
		if amp && c == '&' {
			return false
		}
		if apos && c == '\'' {
			return false
		}
	}
	return true
}

// writePHPQuoted добавляет к результату jsontext.AppendQuote специфику
// json_encode: '/' → \/ (без JSON_UNESCAPED_SLASHES), не-ASCII → \uXXXX
// (сурогатные пары; без JSON_UNESCAPED_UNICODE), флаги JSON_HEX_*,
// JSON_UNESCAPED_LINE_TERMINATORS. Эскейпы control/кавычек/бэкслеша уже сделаны
// stdlib и копируются как есть.
func (e *jsonEncoder) writePHPQuoted(q []byte) {
	hexTag := e.flags&jsonHexTag != 0
	hexAmp := e.flags&jsonHexAmp != 0
	hexApos := e.flags&jsonHexApos != 0
	hexQuot := e.flags&jsonHexQuot != 0
	unescSlash := e.flags&jsonUnescapedSlashes != 0
	unescUni := e.flags&jsonUnescapedUnicode != 0
	lineTerm := e.flags&jsonUnescapedLineTerminators != 0
	e.buf.WriteByte('"')
	for i := 1; i < len(q)-1; i++ {
		c := q[i]
		if c == '\\' {
			// Готовая escape-последовательность: \" или \uXXXX и т.п.
			if i+1 < len(q) && q[i+1] == '"' && hexQuot {
				e.buf.WriteString("\\u0022")
				i++
				continue
			}
			e.buf.WriteByte(c)
			if i+1 < len(q) {
				i++
				e.buf.WriteByte(q[i])
			}
			continue
		}
		if c < 0x80 {
			switch {
			case c == '/':
				if unescSlash {
					e.buf.WriteByte('/')
				} else {
					e.buf.WriteString("\\/")
				}
			case c == '<' && hexTag:
				e.buf.WriteString("\\u003C")
			case c == '>' && hexTag:
				e.buf.WriteString("\\u003E")
			case c == '&' && hexAmp:
				e.buf.WriteString("\\u0026")
			case c == '\'' && hexApos:
				e.buf.WriteString("\\u0027")
			default:
				e.buf.WriteByte(c)
			}
			continue
		}
		r, size := utf8.DecodeRune(q[i:])
		if unescUni {
			if (r == 0x2028 || r == 0x2029) && !lineTerm {
				writeU4(&e.buf, r)
			} else {
				e.buf.Write(q[i : i+size])
			}
		} else if r <= 0xFFFF {
			writeU4(&e.buf, r)
		} else {
			r -= 0x10000
			writeU4(&e.buf, 0xD800+(r>>10))
			writeU4(&e.buf, 0xDC00+(r&0x3FF))
		}
		i += size - 1
	}
	e.buf.WriteByte('"')
}

// jsonTextParser — обход токенов encoding/json/jsontext: синтаксис, кавычки и
// escape'ы (включая суррогатные пары) делает стандартная библиотека; мы строим
// PHP-дерево (упорядоченные массивы/объекты) и переводим ошибки в коды
// json_last_error, как PHP: 3 — управляющий символ, 4 — синтаксис,
// 5 — невалидный UTF-8, 10 — одиночный суррогат.
type jsonTextParser struct {
	vm       *vm.VM
	dec      *jsontext.Decoder
	flags    int64
	maxDepth int
	assoc    bool
	err      int
}

// jsonClassifyError переводит ошибку jsontext в код json_last_error PHP.
func jsonClassifyError(err error) int {
	switch {
	case err == nil, errors.Is(err, io.EOF):
		return jsonErrorSyntax
	case strings.Contains(err.Error(), "invalid UTF-8"):
		return jsonErrorUTF8
	case strings.Contains(err.Error(), "surrogate"):
		return jsonErrorUTF16
	case strings.Contains(err.Error(), "control character"):
		return jsonErrorCtrlChar
	}
	return jsonErrorSyntax
}

// sanitizeInvalidUTF8Raw — то же, но по самим байтам значения (не по
// JSON-тексту): для json_encode (IGNORE выбрасывает, SUBSTITUTE → U+FFFD).
func sanitizeInvalidUTF8Raw(s string, substitute bool) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			if substitute {
				b.WriteRune(0xFFFD)
			}
			i++
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// sanitizeInvalidUTF8 обрабатывает невалидные UTF-8 байты внутри строковых
// литералов ДО jsontext: substitute=false — выбросить (JSON_INVALID_UTF8_IGNORE),
// substitute=true — заменить на U+FFFD (JSON_INVALID_UTF8_SUBSTITUTE). Вне строк
// невалидные байты остаются — их отвергнет парсер (как PHP: Syntax error).
func sanitizeInvalidUTF8(s string, substitute bool) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inStr := false
	for i := 0; i < len(s); {
		c := s[i]
		if !inStr {
			b.WriteByte(c)
			if c == '"' {
				inStr = true
			}
			i++
			continue
		}
		switch {
		case c == '\\':
			b.WriteByte(c)
			if i+1 < len(s) {
				b.WriteByte(s[i+1])
				i += 2
			} else {
				i++
			}
		case c == '"':
			b.WriteByte(c)
			inStr = false
			i++
		case c < 0x80:
			b.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				if substitute {
					b.WriteRune(0xFFFD)
				}
				i++
				continue
			}
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

func (p *jsonTextParser) fail(err error) (vm.Slot, bool) {
	p.err = jsonClassifyError(err)
	return vm.MakeNull(), false
}

func (p *jsonTextParser) parseValue(depth int) (vm.Slot, bool) {
	if depth > p.maxDepth {
		p.err = jsonErrorDepth
		return vm.MakeNull(), false
	}
	kind := p.dec.PeekKind()
	if kind == jsontext.KindInvalid {
		// Значения нет: ошибка синтаксиса либо пустой ввод.
		_, err := p.dec.ReadToken()
		return p.fail(err)
	}
	switch kind {
	case 'n', 't', 'f':
		tok, err := p.dec.ReadToken()
		if err != nil {
			return p.fail(err)
		}
		switch tok.Kind() {
		case 'n':
			return vm.MakeNull(), true
		case 't':
			return vm.MakeBool(true), true
		default:
			return vm.MakeBool(false), true
		}
	case '"':
		tok, err := p.dec.ReadToken()
		if err != nil {
			return p.fail(err)
		}
		return vm.MakeString(tok.String()), true
	case '0':
		tok, err := p.dec.ReadToken()
		if err != nil {
			return p.fail(err)
		}
		if n, ierr := tok.Int(); ierr == nil {
			return vm.MakeInt(n), true
		}
		if p.flags&jsonBigintAsString != 0 {
			return vm.MakeString(tok.String()), true
		}
		f, ferr := tok.Float()
		if ferr != nil {
			return p.fail(ferr)
		}
		return vm.MakeFloat(f), true
	case '[':
		if _, err := p.dec.ReadToken(); err != nil {
			return p.fail(err)
		}
		arr := vm.NewArray()
		for p.dec.PeekKind() != ']' {
			v, ok := p.parseValue(depth + 1)
			if !ok {
				return vm.MakeNull(), false
			}
			_, _ = arr.Append(v)
		}
		if _, err := p.dec.ReadToken(); err != nil {
			return p.fail(err)
		}
		return vm.MakeArray(arr), true
	case '{':
		if _, err := p.dec.ReadToken(); err != nil {
			return p.fail(err)
		}
		order := []string{}
		slots := map[string]vm.Slot{}
		for p.dec.PeekKind() != '}' {
			kt, err := p.dec.ReadToken()
			if err != nil {
				return p.fail(err)
			}
			key := kt.String()
			v, ok := p.parseValue(depth + 1)
			if !ok {
				return vm.MakeNull(), false
			}
			if _, dup := slots[key]; !dup {
				order = append(order, key)
			}
			slots[key] = v
		}
		if _, err := p.dec.ReadToken(); err != nil {
			return p.fail(err)
		}
		if p.assoc {
			arr := vm.NewArray()
			for _, k := range order {
				// Числовые строки канонизируются в int-ключи, как PHP-массив.
				_ = arr.Set(vm.KeyFromString(k), slots[k])
			}
			return vm.MakeArray(arr), true
		}
		props := make(map[string]vm.Value, len(order))
		for _, k := range order {
			props[k] = slots[k]
		}
		return makeStdClass(p.vm, order, props), true
	}
	return p.fail(nil)
}

func jsonDecodeFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	flags := int64(0)
	if len(ctx.Args()) >= 4 && !arg(ctx, 3).IsNull() {
		flags = arg(ctx, 3).ToInt()
	}
	assoc := flags&jsonObjectAsArray != 0
	if len(ctx.Args()) >= 2 && !arg(ctx, 1).IsNull() {
		assoc = arg(ctx, 1).ToBool()
	}
	depth := 512
	if len(ctx.Args()) >= 3 && !arg(ctx, 2).IsNull() {
		depth = int(arg(ctx, 2).ToInt())
	}
	// Флаги невалидного UTF-8 обрабатываем ДО парсера, по строковым литералам:
	// IGNORE выбрасывает байты, SUBSTITUTE заменяет на U+FFFD (php_base64-style
	// точность; jsontext умеет только замену).
	if flags&jsonInvalidUTF8Ignore != 0 {
		s = sanitizeInvalidUTF8(s, false)
	} else if flags&jsonInvalidUTF8Substitute != 0 {
		s = sanitizeInvalidUTF8(s, true)
	}
	// AllowDuplicateNames — как PHP: последнее значение побеждает.
	dec := jsontext.NewDecoder(strings.NewReader(s), jsontext.AllowDuplicateNames(true))
	p := &jsonTextParser{vm: ctx.VM(), dec: dec, flags: flags, maxDepth: depth, assoc: assoc}
	v, ok := p.parseValue(0)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !ok {
		jsonLastError = p.err
		return vm.MakeNull()
	}
	// После значения допускается только пробел.
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		jsonLastError = jsonClassifyError(err)
		return vm.MakeNull()
	}
	jsonLastError = 0
	return v
}

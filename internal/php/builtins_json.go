package php

import (
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
	hexTag := e.flags&jsonHexTag != 0
	hexAmp := e.flags&jsonHexAmp != 0
	hexApos := e.flags&jsonHexApos != 0
	hexQuot := e.flags&jsonHexQuot != 0
	unescSlash := e.flags&jsonUnescapedSlashes != 0
	unescUni := e.flags&jsonUnescapedUnicode != 0
	lineTerm := e.flags&jsonUnescapedLineTerminators != 0
	e.buf.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			switch {
			case c == '"':
				if hexQuot {
					e.buf.WriteString("\\u0022")
				} else {
					e.buf.WriteString("\\\"")
				}
			case c == '\\':
				e.buf.WriteString("\\\\")
			case c == '/':
				if unescSlash {
					e.buf.WriteByte('/')
				} else {
					e.buf.WriteString("\\/")
				}
			case c == '\n':
				e.buf.WriteString("\\n")
			case c == '\r':
				e.buf.WriteString("\\r")
			case c == '\t':
				e.buf.WriteString("\\t")
			case c == '\b':
				e.buf.WriteString("\\b")
			case c == '\f':
				e.buf.WriteString("\\f")
			case c == '<' && hexTag:
				e.buf.WriteString("\\u003C")
			case c == '>' && hexTag:
				e.buf.WriteString("\\u003E")
			case c == '&' && hexAmp:
				e.buf.WriteString("\\u0026")
			case c == '\'' && hexApos:
				e.buf.WriteString("\\u0027")
			case c < 0x20:
				writeU4(&e.buf, rune(c))
			default:
				e.buf.WriteByte(c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			if e.flags&jsonInvalidUTF8Ignore != 0 {
				i++
				continue
			}
			if e.flags&jsonInvalidUTF8Substitute != 0 {
				e.buf.WriteString("\\ufffd")
				i++
				continue
			}
			e.err = jsonErrorUTF8
			return false
		}
		if unescUni {
			if (r == 0x2028 || r == 0x2029) && !lineTerm {
				writeU4(&e.buf, r)
			} else {
				e.buf.WriteString(s[i : i+size])
			}
		} else {
			if r <= 0xFFFF {
				writeU4(&e.buf, r)
			} else {
				r -= 0x10000
				writeU4(&e.buf, 0xD800+(r>>10))
				writeU4(&e.buf, 0xDC00+(r&0x3FF))
			}
		}
		i += size
	}
	e.buf.WriteByte('"')
	return true
}

// --- JSON DECODE ---

type jsonParser struct {
	vm       *vm.VM
	s        string
	i        int
	flags    int64
	maxDepth int
	assoc    bool
	err      int
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
	p := &jsonParser{vm: ctx.VM(), s: s, flags: flags, maxDepth: depth, assoc: assoc}
	v, ok := p.parseValue(0)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !ok {
		jsonLastError = p.err
		return vm.MakeNull()
	}
	p.skipWS()
	if p.i != len(p.s) {
		jsonLastError = jsonErrorSyntax
		return vm.MakeNull()
	}
	jsonLastError = 0
	return v
}

func (p *jsonParser) skipWS() {
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			p.i++
		} else {
			break
		}
	}
}

func hex4(s string) int {
	v := 0
	for i := 0; i < 4; i++ {
		c := s[i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return -1
		}
		v = v<<4 | d
	}
	return v
}

func (p *jsonParser) parseValue(depth int) (vm.Slot, bool) {
	if depth > p.maxDepth {
		p.err = jsonErrorDepth
		return vm.MakeNull(), false
	}
	p.skipWS()
	if p.i >= len(p.s) {
		p.err = jsonErrorSyntax
		return vm.MakeNull(), false
	}
	c := p.s[p.i]
	switch {
	case c == 'n':
		if strings.HasPrefix(p.s[p.i:], "null") {
			p.i += 4
			return vm.MakeNull(), true
		}
	case c == 't':
		if strings.HasPrefix(p.s[p.i:], "true") {
			p.i += 4
			return vm.MakeBool(true), true
		}
	case c == 'f':
		if strings.HasPrefix(p.s[p.i:], "false") {
			p.i += 5
			return vm.MakeBool(false), true
		}
	case c == '"':
		return p.parseString()
	case c == '[':
		return p.parseArray(depth)
	case c == '{':
		return p.parseObject(depth)
	case c == '-' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	}
	p.err = jsonErrorSyntax
	return vm.MakeNull(), false
}

func (p *jsonParser) parseString() (vm.Slot, bool) {
	p.i++ // "
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '"' {
			p.i++
			return vm.MakeString(b.String()), true
		}
		if c == '\\' {
			p.i++
			if p.i >= len(p.s) {
				break
			}
			esc := p.s[p.i]
			switch esc {
			case '"':
				b.WriteByte('"')
				p.i++
			case '\\':
				b.WriteByte('\\')
				p.i++
			case '/':
				b.WriteByte('/')
				p.i++
			case 'b':
				b.WriteByte('\b')
				p.i++
			case 'f':
				b.WriteByte('\f')
				p.i++
			case 'n':
				b.WriteByte('\n')
				p.i++
			case 'r':
				b.WriteByte('\r')
				p.i++
			case 't':
				b.WriteByte('\t')
				p.i++
			case 'u':
				if p.i+4 >= len(p.s) {
					p.err = jsonErrorSyntax
					return vm.MakeNull(), false
				}
				h := hex4(p.s[p.i+1 : p.i+5])
				if h < 0 {
					p.err = jsonErrorSyntax
					return vm.MakeNull(), false
				}
				p.i += 5
				r := rune(h)
				if r >= 0xD800 && r <= 0xDBFF {
					if p.i+5 < len(p.s) && p.s[p.i] == '\\' && p.s[p.i+1] == 'u' {
						h2 := hex4(p.s[p.i+2 : p.i+6])
						if h2 >= 0xDC00 && h2 <= 0xDFFF {
							r = 0x10000 + (r-0xD800)<<10 + (rune(h2) - 0xDC00)
							p.i += 6
						} else {
							p.err = jsonErrorUTF16
							return vm.MakeNull(), false
						}
					} else {
						p.err = jsonErrorUTF16
						return vm.MakeNull(), false
					}
				} else if r >= 0xDC00 && r <= 0xDFFF {
					p.err = jsonErrorUTF16
					return vm.MakeNull(), false
				}
				b.WriteRune(r)
			default:
				p.err = jsonErrorSyntax
				return vm.MakeNull(), false
			}
			continue
		}
		if c < 0x20 {
			p.err = jsonErrorCtrlChar
			return vm.MakeNull(), false
		}
		b.WriteByte(c)
		p.i++
	}
	p.err = jsonErrorSyntax
	return vm.MakeNull(), false
}

func (p *jsonParser) parseNumber() (vm.Slot, bool) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	isFloat := false
	if p.i < len(p.s) && p.s[p.i] == '.' {
		isFloat = true
		p.i++
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		isFloat = true
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	}
	tok := p.s[start:p.i]
	if tok == "" || tok == "-" {
		p.err = jsonErrorSyntax
		return vm.MakeNull(), false
	}
	if !isFloat {
		if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
			return vm.MakeInt(n), true
		}
		if p.flags&jsonBigintAsString != 0 {
			return vm.MakeString(tok), true
		}
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		p.err = jsonErrorSyntax
		return vm.MakeNull(), false
	}
	return vm.MakeFloat(f), true
}

func (p *jsonParser) parseArray(depth int) (vm.Slot, bool) {
	p.i++ // [
	arr := vm.NewArray()
	p.skipWS()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return vm.MakeArray(arr), true
	}
	for {
		v, ok := p.parseValue(depth + 1)
		if !ok {
			return vm.MakeNull(), false
		}
		arr.Append(v)
		p.skipWS()
		if p.i >= len(p.s) {
			p.err = jsonErrorSyntax
			return vm.MakeNull(), false
		}
		if p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.s[p.i] == ']' {
			p.i++
			return vm.MakeArray(arr), true
		}
		p.err = jsonErrorSyntax
		return vm.MakeNull(), false
	}
}

func (p *jsonParser) parseObject(depth int) (vm.Slot, bool) {
	p.i++ // {
	props := map[string]vm.Value{}
	order := []string{}
	p.skipWS()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		if p.assoc {
			return vm.MakeArray(vm.NewArray()), true
		}
		return makeStdClass(p.vm, order, props), true
	}
	for {
		p.skipWS()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			p.err = jsonErrorSyntax
			return vm.MakeNull(), false
		}
		k, ok := p.parseString()
		if !ok {
			return vm.MakeNull(), false
		}
		key := k.ToString()
		p.skipWS()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			p.err = jsonErrorSyntax
			return vm.MakeNull(), false
		}
		p.i++
		v, ok := p.parseValue(depth + 1)
		if !ok {
			return vm.MakeNull(), false
		}
		if _, dup := props[key]; !dup {
			order = append(order, key)
		}
		props[key] = v
		p.skipWS()
		if p.i >= len(p.s) {
			p.err = jsonErrorSyntax
			return vm.MakeNull(), false
		}
		if p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.s[p.i] == '}' {
			p.i++
			break
		}
		p.err = jsonErrorSyntax
		return vm.MakeNull(), false
	}
	if p.assoc {
		arr := vm.NewArray()
		for _, k := range order {
			arr.SetStr(k, slotFromValue(props[k]))
		}
		return vm.MakeArray(arr), true
	}
	return makeStdClass(p.vm, order, props), true
}

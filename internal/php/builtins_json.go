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

// jsonEncoder отдаёт значения jsontext.Encoder: всю запись JSON (синтаксис,
// разделители, отступы, кавычки, экранирование строк, формат чисел) ведёт
// stdlib; наш код только обходит PHP-значения и вызывает методы энкодера —
// собственных записей байтов в буфер нет (out — приёмник вывода энкодера).
type jsonEncoder struct {
	vm       *vm.VM
	ctx      *vm.Context
	out      strings.Builder
	enc      *jsontext.Encoder
	flags    int64
	maxDepth int
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
	e := &jsonEncoder{vm: ctx.VM(), ctx: ctx, flags: flags, maxDepth: depth}
	// Грубая оценка выходного размера (единственный накопитель — итоговая строка).
	root := arg0(ctx)
	if root.Type() == vm.ArrayType {
		if n := root.AsArray().Len(); n > 0 {
			if n > 1<<18 {
				e.out.Grow(1 << 20)
			} else {
				e.out.Grow(2 + 16*n)
			}
		}
	}
	// Опции энкодера: pretty-раскладка как у PHP (4 пробела, ": "), SUBSTITUTE
	// невалидного UTF-8 — AllowInvalidUTF8 (U+FFFD вместо ошибки).
	var opts []jsontext.Options
	if flags&jsonPrettyPrint != 0 {
		opts = append(opts, jsontext.Multiline(true), jsontext.WithIndent("    "), jsontext.SpaceAfterColon(true))
	}
	if flags&jsonInvalidUTF8Substitute != 0 {
		// U+FFFD вместо ошибки — php-семантика JSON_INVALID_UTF8_SUBSTITUTE.
		opts = append(opts, jsontext.AllowInvalidUTF8(true))
	}
	if flags&(jsonHexTag|jsonHexAmp) != 0 {
		// JSON_HEX_TAG/HEX_AMP: '<', '>' и '&' уходят как \uXXXX (stdlib).
		opts = append(opts, jsontext.EscapeForHTML(true))
	}
	if flags&jsonUnescapedUnicode == 0 || flags&jsonUnescapedLineTerminators == 0 {
		// U+2028/2029 php экранирует, кроме JSON_UNESCAPED_LINE_TERMINATORS
		// при включённом JSON_UNESCAPED_UNICODE.
		opts = append(opts, jsontext.EscapeForJS(true))
	}
	e.enc = jsontext.NewEncoder(&e.out, opts...)
	if !e.encode(root) {
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
	out := e.out.String()
	// Потоковый Encoder завершает документ переводом строки; json_encode его не
	// отдаёт (json.Marshal снимает его внутренним OmitTopLevelNewline —
	// публичной опции нет, поэтому срезаем у готовой строки).
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return vm.Str(out)
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
		return e.writeToken(jsontext.Null)
	case vm.BoolType:
		return e.writeToken(jsontext.Bool(v.ToBool()))
	case vm.IntType:
		return e.writeToken(jsontext.Int(v.ToInt()))
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
	// Число передаём готовым литералом через jsontext.Value: формат PHP
	// (1.0e+25, 42.0 при JSON_PRESERVE_ZERO_FRACTION) stdlib не выражает ни
	// одной опцией. Это value-уровень (аналог json.RawMessage) — структуру,
	// строки и экранирование по-прежнему пишет jsontext.
	s := phpFormatFloat(f, e.flags&jsonPreserveZeroFraction != 0)
	s = strings.Replace(s, "E", "e", 1)
	if err := e.enc.WriteValue(jsontext.Value(s)); err != nil {
		e.err = jsonErrorUTF8
		return false
	}
	return true
}

func (e *jsonEncoder) encodeNumericString(s string) bool {
	if strings.ContainsAny(s, ".eE") {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return e.encodeFloat(f)
		}
	} else {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return e.writeToken(jsontext.Int(n))
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
		if !e.writeToken(jsontext.BeginArray) {
			return false
		}
		for _, v := range a.All() {
			if !e.encodeDepth(v, depth+1) {
				return false
			}
		}
		return e.writeToken(jsontext.EndArray)
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
	if !e.writeToken(jsontext.BeginObject) {
		return false
	}
	for _, en := range ents {
		if !e.encodeString(en.key) {
			return false
		}
		if !e.encodeDepth(en.val, depth+1) {
			return false
		}
	}
	return e.writeToken(jsontext.EndObject)
}

// writeToken отдаёт токен jsontext.Encoder: синтаксис, кавычки и экранирование
// строк делает stdlib. Единственная ошибка здесь — невалидный UTF-8 в строке
// (без JSON_INVALID_UTF8_*): для json_encode это false / JsonException.
func (e *jsonEncoder) writeToken(t jsontext.Token) bool {
	if err := e.enc.WriteToken(t); err != nil {
		e.err = jsonErrorUTF8
		return false
	}
	return true
}

func (e *jsonEncoder) encodeString(s string) bool {
	// IGNORE: невалидные байты выбрасываем до stdlib. SUBSTITUTE делает опция
	// AllowInvalidUTF8 (U+FFFD); по умолчанию WriteToken вернёт ошибку — это
	// php-семантика false для JSON_ERROR_UTF8.
	if e.flags&jsonInvalidUTF8Ignore != 0 {
		s = sanitizeInvalidUTF8Raw(s, false)
	}
	return e.writeToken(jsontext.String(s))
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
// jsontext оборачивает грамматические ошибки в *SyntacticError и дописывает к
// тексту JSON-указатель (имена ключей!) и офсет, поэтому классифицируем только
// по вложенной ошибке: иначе ключ с именем "surrogate" или "invalid UTF-8"
// менял бы код ошибки (3/4/5/10).
func jsonClassifyError(err error) int {
	switch {
	case err == nil, errors.Is(err, io.EOF):
		return jsonErrorSyntax
	}
	var serr *jsontext.SyntacticError
	if errors.As(err, &serr) && serr.Err != nil {
		err = serr.Err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "invalid UTF-8"):
		return jsonErrorUTF8
	case strings.Contains(msg, "surrogate"):
		return jsonErrorUTF16
	case strings.Contains(msg, "control character"):
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

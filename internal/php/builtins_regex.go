package php

import (
	"fmt"
	"phpvm/internal/vm"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Файл содержит реализацию preg_*.

// =====================================================================
// preg_* — PCRE-подобный API поверх Go regexp (RE2).
//
// Перевод PHP-паттерна: делимитер (первый символ; пары (){}[]<> — с учётом
// вложенности), флаги после закрывающего делимитера. Поддерживаются i,m,s,x,
// A,D,S,U,X,J,u. Флаги i/m/s/U транслируются во встроенные флаги RE2; x
// реализован препроцессором (снос пробелов и #-комментариев вне классов);
// A — префикс \A. D/S/X/J/u — no-op (Go и так байтово-строгий/UTF-8).
//
// RE2 не поддерживает lookaround, backreferences, атомарные группы и
// рекурсию — такие паттерны дают ошибку компиляции: функции возвращают false,
// а preg_last_error() = PREG_INTERNAL_ERROR. Паники не бывает.
// =====================================================================

const (
	// Флаги preg_match/preg_match_all.
	pregOffsetCapture   = 256
	pregUnmatchedAsNull = 512
	// Флаги preg_split.
	pregSplitNoEmpty       = 1
	pregSplitDelimCapture  = 2
	pregSplitOffsetCapture = 4
	// Флаги preg_grep.
	pregGrepInvert = 1
	// Коды preg_last_error.
	pregErrNoError    = 0
	pregErrInternal   = 1
	pregErrBacktrack  = 2
	pregErrRecursion  = 3
	pregErrBadUTF8    = 4
	pregErrBadUTF8Off = 5
	pregErrJITStack   = 6
)

// pregLastError — пакетное состояние последней ошибки preg_* (как в PHP).
var pregLastError = pregErrNoError

func pregLastErrorMsg(code int) string {
	switch code {
	case pregErrNoError:
		return "No error"
	case pregErrInternal:
		return "Internal error"
	case pregErrBacktrack:
		return "Backtrack limit exhausted"
	case pregErrRecursion:
		return "Recursion limit exhausted"
	case pregErrBadUTF8:
		return "Malformed UTF-8 characters, possibly incorrectly encoded"
	case pregErrBadUTF8Off:
		return "The offset did not correspond to the beginning of a valid UTF-8 code point"
	case pregErrJITStack:
		return "JIT stack limit exhausted"
	}
	return "Unknown error"
}

// pregWriteRef записывает значение в by-ref аргумент #i (если он есть).
func pregWriteRef(ctx *vm.Context, i int, v vm.Value) {
	args := ctx.Args()
	if i < 0 || i >= len(args) {
		return
	}
	s := args[i]
	if s.IsRef() && s.Ref() != nil {
		var out vm.Slot
		vm.SlotOf(&out, v)
		s.Ref().Set(out)
	}
}

// pregCompile разбирает PHP-паттерн, транслирует его в RE2 и компилирует.
// Возвращает скомпилированное выражение и признак флага u.
func pregCompile(pattern string) (*regexp.Regexp, bool, error) {
	body, mods, err := pregSplitPattern(pattern)
	if err != nil {
		return nil, false, err
	}
	src, err := pregTranslate(body, mods)
	if err != nil {
		return nil, false, err
	}
	re, err := regexp.Compile(src)
	if err != nil {
		return nil, false, err
	}
	return re, strings.ContainsRune(mods, 'u'), nil
}

// pregSplitPattern отделяет тело регулярки от делимитера и модификаторов.
func pregSplitPattern(pattern string) (string, string, error) {
	if len(pattern) < 2 {
		return "", "", fmt.Errorf("Empty regular expression")
	}
	open := pattern[0]
	close := open
	switch open {
	case '(':
		close = ')'
	case '{':
		close = '}'
	case '[':
		close = ']'
	case '<':
		close = '>'
	}
	end := -1
	if close == open {
		for i := 1; i < len(pattern); i++ {
			c := pattern[i]
			if c == '\\' {
				i++
				continue
			}
			if c == close {
				end = i
				break
			}
		}
	} else {
		depth := 1
		for i := 1; i < len(pattern); i++ {
			c := pattern[i]
			if c == '\\' {
				i++
				continue
			}
			if c == open {
				depth++
			} else if c == close {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
	}
	if end < 0 {
		return "", "", fmt.Errorf("No ending delimiter %q found", string(close))
	}
	mods := pattern[end+1:]
	for i := 0; i < len(mods); i++ {
		if !strings.ContainsRune("imsxADSUXJu", rune(mods[i])) {
			return "", "", fmt.Errorf("Unknown modifier %q", string(mods[i]))
		}
	}
	return pattern[1:end], mods, nil
}

// pregTranslate собирает итоговый RE2-паттерн из тела и модификаторов.
func pregTranslate(body, mods string) (string, error) {
	var flags strings.Builder
	freeSpacing := false
	for i := 0; i < len(mods); i++ {
		switch mods[i] {
		case 'i', 'm', 's', 'U':
			flags.WriteByte(mods[i])
		case 'x':
			freeSpacing = true
		}
	}
	translated, err := pregTranslateBody(body, freeSpacing)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if flags.Len() > 0 {
		b.WriteString("(?")
		b.WriteString(flags.String())
		b.WriteByte(')')
	}
	b.WriteString(translated)
	src := b.String()
	if strings.ContainsRune(mods, 'A') {
		src = `\A(?:` + src + `)`
	}
	return src, nil
}

// pregTranslateBody переводит PCRE-специфичные escape-последовательности в
// RE2-эквиваленты и (при флаге x) сносит пробелы и #-комментарии.
func pregTranslateBody(s string, freeSpacing bool) (string, error) {
	var b strings.Builder
	inClass := false
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				b.WriteByte('\\')
				i++
				continue
			}
			n := s[i+1]
			if !inClass {
				switch n {
				case 'R':
					b.WriteString(`(?:\r\n|[\n\r\v\f\x{85}\x{2028}\x{2029}])`)
					i += 2
					continue
				case 'h':
					b.WriteString(`[ \t\x{00A0}\x{1680}\x{2000}-\x{200A}\x{202F}\x{205F}\x{3000}]`)
					i += 2
					continue
				case 'H':
					b.WriteString(`[^ \t\x{00A0}\x{1680}\x{2000}-\x{200A}\x{202F}\x{205F}\x{3000}]`)
					i += 2
					continue
				case 'Z':
					b.WriteString(`\z`)
					i += 2
					continue
				}
			}
			b.WriteByte('\\')
			b.WriteByte(n)
			i += 2
			continue
		}
		if inClass {
			if c == ']' {
				inClass = false
			}
			b.WriteByte(c)
			i++
			continue
		}
		if c == '[' {
			inClass = true
			b.WriteByte(c)
			i++
			continue
		}
		if freeSpacing {
			switch c {
			case ' ', '\t', '\n', '\r', '\f', '\v':
				i++
				continue
			case '#':
				for i < len(s) && s[i] != '\n' {
					i++
				}
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), nil
}

// pregResolveOffset приводит PHP-offset (в т.ч. отрицательный) к байтовому.
func pregResolveOffset(subject string, offset int64) (int, error) {
	n := int64(len(subject))
	if offset < 0 {
		offset = n + offset
		if offset < 0 {
			return 0, fmt.Errorf("Offset not contained in string")
		}
	}
	if offset > n {
		return 0, fmt.Errorf("Offset not contained in string")
	}
	return int(offset), nil
}

// pregBuildMatches строит PHP-массив совпадений из пар индексов RE2.
func pregBuildMatches(re *regexp.Regexp, subject string, m []int, flags int64) *vm.Array {
	out := vm.NewArray()
	names := re.SubexpNames()
	ngroups := len(m) / 2
	limit := ngroups
	if flags&pregUnmatchedAsNull == 0 {
		// По умолчанию PHP опускает хвостовые несматченные группы.
		limit = 1
		for i := 1; i < ngroups; i++ {
			if m[2*i] >= 0 {
				limit = i + 1
			}
		}
	}
	for i := 0; i < limit; i++ {
		val := pregMatchValue(subject, m[2*i], m[2*i+1], flags)
		if i > 0 && i < len(names) && names[i] != "" {
			out.SetStr(names[i], val)
		}
		out.SetInt(int64(i), val)
	}
	return out
}

// pregMatchValue кодирует одну группу совпадения с учётом флагов.
func pregMatchValue(subject string, start, end int, flags int64) vm.Slot {
	if flags&pregOffsetCapture != 0 {
		pair := vm.NewArray()
		if start < 0 {
			if flags&pregUnmatchedAsNull != 0 {
				pair.Append(vm.MakeNull())
			} else {
				pair.Append(vm.MakeString(""))
			}
			pair.Append(vm.MakeInt(-1))
			return vm.MakeArray(pair)
		}
		pair.Append(vm.MakeString(subject[start:end]))
		pair.Append(vm.MakeInt(int64(start)))
		return vm.MakeArray(pair)
	}
	if start < 0 {
		if flags&pregUnmatchedAsNull != 0 {
			return vm.MakeNull()
		}
		return vm.MakeString("")
	}
	return vm.MakeString(subject[start:end])
}

// pregGroup возвращает текст группы n (или "" для отсутствующей/несматченной).
func pregGroup(subject string, m []int, n int) string {
	if n < 0 || 2*n+1 >= len(m) {
		return ""
	}
	s, e := m[2*n], m[2*n+1]
	if s < 0 {
		return ""
	}
	return subject[s:e]
}

func pregIsWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// pregExpand применяет PHP-подстановки (\1, $1, ${1}) в replacement.
// Именованные группы PHP в replacement не раскрывает — они остаются как есть.
func pregExpand(repl string, re *regexp.Regexp, subject string, m []int) string {
	var b strings.Builder
	i := 0
	for i < len(repl) {
		c := repl[i]
		if c == '\\' {
			if i+1 >= len(repl) {
				b.WriteByte('\\')
				i++
				continue
			}
			n := repl[i+1]
			if n == '\\' {
				b.WriteByte('\\')
				i += 2
				continue
			}
			if n >= '0' && n <= '9' {
				j := i + 1
				num := 0
				for j < len(repl) && j < i+3 && repl[j] >= '0' && repl[j] <= '9' {
					num = num*10 + int(repl[j]-'0')
					j++
				}
				b.WriteString(pregGroup(subject, m, num))
				i = j
				continue
			}
			// Неизвестный escape PHP оставляет как есть (напр. \q).
			b.WriteByte('\\')
			b.WriteByte(n)
			i += 2
			continue
		}
		if c != '$' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(repl) {
			b.WriteByte('$')
			i++
			continue
		}
		n := repl[i+1]
		if n == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		if n == '{' {
			j := i + 2
			for j < len(repl) && repl[j] != '}' {
				j++
			}
			tok := repl[i+2 : j]
			if gi, ok := pregTokenIndex(tok); ok {
				b.WriteString(pregGroup(subject, m, gi))
			} else {
				b.WriteString("${")
				b.WriteString(tok)
				if j < len(repl) {
					b.WriteByte('}')
				}
			}
			if j < len(repl) {
				i = j + 1
			} else {
				i = j
			}
			continue
		}
		if n >= '0' && n <= '9' {
			j := i + 1
			num := 0
			for j < len(repl) && repl[j] >= '0' && repl[j] <= '9' {
				num = num*10 + int(repl[j]-'0')
				j++
			}
			b.WriteString(pregGroup(subject, m, num))
			i = j
			continue
		}
		j := i + 1
		for j < len(repl) && pregIsWordByte(repl[j]) {
			j++
		}
		tok := repl[i+1 : j]
		if gi, ok := pregTokenIndex(tok); ok {
			b.WriteString(pregGroup(subject, m, gi))
		} else {
			b.WriteByte('$')
			b.WriteString(tok)
		}
		i = j
	}
	return b.String()
}

// pregTokenIndex по токену подстановки возвращает номер группы. PHP не умеет
// ссылаться на именованные группы в replacement (${name}/$name остаются как
// есть), поэтому распознаются только числовые токены.
func pregTokenIndex(tok string) (int, bool) {
	if tok == "" {
		return 0, false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return 0, false
		}
	}
	n := 0
	for i := 0; i < len(tok); i++ {
		n = n*10 + int(tok[i]-'0')
	}
	return n, true
}

// pregReplaceAll заменяет до limit совпадений (\1/$1/${1} в replacement).
func pregReplaceAll(re *regexp.Regexp, repl, subject string, limit int64) (string, int) {
	ms := re.FindAllStringSubmatchIndex(subject, -1)
	if len(ms) == 0 {
		return subject, 0
	}
	if limit >= 0 && int64(len(ms)) > limit {
		ms = ms[:limit]
	}
	var b strings.Builder
	prev := 0
	for _, m := range ms {
		b.WriteString(subject[prev:m[0]])
		b.WriteString(pregExpand(repl, re, subject, m))
		prev = m[1]
	}
	b.WriteString(subject[prev:])
	return b.String(), len(ms)
}

// pregQuoteFn реализует preg_quote.
func pregQuoteFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	delim := ""
	if len(ctx.Args()) >= 2 && !arg(ctx, 1).IsNull() {
		delim = arg(ctx, 1).ToString()
	}
	const special = `.\+*?[^]$(){}=!<>|:-#`
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0 || strings.IndexByte(special, c) >= 0 || (delim != "" && strings.IndexByte(delim, c) >= 0) {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return vm.Str(b.String())
}

// pregMatchFn реализует preg_match.
func pregMatchFn(ctx *vm.Context) vm.Value {
	pregLastError = pregErrNoError
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("preg_match() expects at least 2 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	re, utf8mode, err := pregCompile(arg(ctx, 0).ToString())
	if err != nil {
		pregLastError = pregErrInternal
		return vm.Bool(false)
	}
	subject := arg(ctx, 1).ToString()
	flags := int64(0)
	if len(args) >= 4 {
		flags = arg(ctx, 3).ToInt()
	}
	offset := int64(0)
	if len(args) >= 5 {
		offset = arg(ctx, 4).ToInt()
	}
	off, oerr := pregResolveOffset(subject, offset)
	if oerr != nil {
		pregLastError = pregErrInternal
		return vm.Bool(false)
	}
	if utf8mode && !utf8.ValidString(subject) {
		pregLastError = pregErrBadUTF8
		return vm.Bool(false)
	}
	m := re.FindStringSubmatchIndex(subject[off:])
	if m == nil {
		pregWriteRef(ctx, 2, vm.MakeArray(vm.NewArray()))
		return vm.Int(0)
	}
	for i := range m {
		if m[i] >= 0 {
			m[i] += off
		}
	}
	pregWriteRef(ctx, 2, vm.MakeArray(pregBuildMatches(re, subject, m, flags)))
	return vm.Int(1)
}

// pregMatchAllFn реализует preg_match_all.
func pregMatchAllFn(ctx *vm.Context) vm.Value {
	pregLastError = pregErrNoError
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("preg_match_all() expects at least 2 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	re, utf8mode, err := pregCompile(arg(ctx, 0).ToString())
	if err != nil {
		pregLastError = pregErrInternal
		return vm.Bool(false)
	}
	subject := arg(ctx, 1).ToString()
	flags := int64(0)
	if len(args) >= 4 {
		flags = arg(ctx, 3).ToInt()
	}
	offset := int64(0)
	if len(args) >= 5 {
		offset = arg(ctx, 4).ToInt()
	}
	off, oerr := pregResolveOffset(subject, offset)
	if oerr != nil {
		pregLastError = pregErrInternal
		return vm.Bool(false)
	}
	if utf8mode && !utf8.ValidString(subject) {
		pregLastError = pregErrBadUTF8
		return vm.Bool(false)
	}
	all := re.FindAllStringSubmatchIndex(subject[off:], -1)
	out := vm.NewArray()
	ngroups := re.NumSubexp() + 1
	groupVals := make([]*vm.Array, ngroups)
	for g := range groupVals {
		groupVals[g] = vm.NewArray()
	}
	for _, m := range all {
		for g := 0; g < ngroups; g++ {
			s, e := m[2*g], m[2*g+1]
			if s >= 0 {
				s += off
				e += off
			}
			groupVals[g].Append(pregMatchValue(subject, s, e, flags))
		}
	}
	names := re.SubexpNames()
	for g := 0; g < ngroups; g++ {
		if g > 0 && g < len(names) && names[g] != "" {
			out.SetStr(names[g], vm.MakeArray(groupVals[g]))
		}
		out.SetInt(int64(g), vm.MakeArray(groupVals[g]))
	}
	pregWriteRef(ctx, 2, vm.MakeArray(out))
	return vm.Int(int64(len(all)))
}

// pregReplaceFn реализует preg_replace (массивы pattern/replacement/subject).
func pregReplaceFn(ctx *vm.Context) vm.Value {
	pregLastError = pregErrNoError
	args := ctx.Args()
	if len(args) < 3 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("preg_replace() expects at least 3 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	patSlot, replSlot, subjSlot := arg(ctx, 0), arg(ctx, 1), arg(ctx, 2)
	limit := int64(-1)
	if len(args) >= 4 {
		limit = arg(ctx, 3).ToInt()
	}
	patArr := patSlot.Type() == vm.ArrayType
	replArr := replSlot.Type() == vm.ArrayType
	subjArr := subjSlot.Type() == vm.ArrayType
	if replSlot.Type() == vm.ClosureType || replSlot.Type() == vm.ObjectType {
		ctx.Throw(vm.NewThrowable("TypeError",
			"preg_replace(): Argument #2 ($replacement) must be of type array|string, "+phpTypeName(ctx, 1)+" given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if replArr && !patArr {
		ctx.Throw(vm.NewThrowable("TypeError",
			"preg_replace(): Argument #1 ($pattern) must be of type array when argument #2 ($replacement) is an array, string given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	var pats []string
	if patArr {
		for _, v := range patSlot.AsArray().All() {
			pats = append(pats, v.ToString())
		}
	} else {
		pats = []string{patSlot.ToString()}
	}
	var repls []string
	if replArr {
		for _, v := range replSlot.AsArray().All() {
			repls = append(repls, v.ToString())
		}
	} else {
		s := replSlot.ToString()
		repls = make([]string, len(pats))
		for i := range repls {
			repls[i] = s
		}
	}
	res := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		re, _, err := pregCompile(p)
		if err != nil {
			pregLastError = pregErrInternal
			return vm.Bool(false)
		}
		res[i] = re
	}
	total := int64(0)
	replaceOne := func(s string) string {
		for i, re := range res {
			r := ""
			if i < len(repls) {
				r = repls[i]
			}
			var n int
			s, n = pregReplaceAll(re, r, s, limit)
			total += int64(n)
		}
		return s
	}
	var result vm.Value
	if subjArr {
		out := vm.NewArray()
		for k, v := range subjSlot.AsArray().All() {
			out.Set(k, vm.MakeString(replaceOne(v.ToString())))
		}
		result = vm.MakeArray(out)
	} else {
		result = vm.MakeString(replaceOne(subjSlot.ToString()))
	}
	pregWriteRef(ctx, 4, vm.MakeInt(total))
	return result
}

// pregReplaceCallbackAll заменяет совпадения результатом callback.
func pregReplaceCallbackAll(ctx *vm.Context, re *regexp.Regexp, cb vm.Slot, subject string, limit int64) (string, int) {
	ms := re.FindAllStringSubmatchIndex(subject, -1)
	if len(ms) == 0 {
		return subject, 0
	}
	if limit >= 0 && int64(len(ms)) > limit {
		ms = ms[:limit]
	}
	var b strings.Builder
	prev := 0
	for _, m := range ms {
		b.WriteString(subject[prev:m[0]])
		matchArr := pregBuildMatches(re, subject, m, 0)
		r, err := ctx.VMInstance().CallCallable(ctx, cb, []vm.Slot{vm.MakeArray(matchArr)})
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return *new(string), *new(int)
		}
		b.WriteString(r.ToString())
		prev = m[1]
	}
	b.WriteString(subject[prev:])
	return b.String(), len(ms)
}

// pregReplaceCallbackFn реализует preg_replace_callback.
func pregReplaceCallbackFn(ctx *vm.Context) vm.Value {
	pregLastError = pregErrNoError
	args := ctx.Args()
	if len(args) < 3 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("preg_replace_callback() expects at least 3 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	patSlot, cb, subjSlot := arg(ctx, 0), arg(ctx, 1), arg(ctx, 2)
	if t := subjSlot.Type(); t != vm.ArrayType && t != vm.StringType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("preg_replace_callback", 3, "subject", "array|string", phpTypeName(ctx, 2), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	limit := int64(-1)
	if len(args) >= 4 {
		limit = arg(ctx, 3).ToInt()
	}
	patArr := patSlot.Type() == vm.ArrayType
	subjArr := subjSlot.Type() == vm.ArrayType
	var pats []string
	if patArr {
		for _, v := range patSlot.AsArray().All() {
			pats = append(pats, v.ToString())
		}
	} else {
		pats = []string{patSlot.ToString()}
	}
	res := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		re, _, err := pregCompile(p)
		if err != nil {
			pregLastError = pregErrInternal
			return vm.Bool(false)
		}
		res[i] = re
	}
	total := int64(0)
	replaceOne := func(s string) string {
		for _, re := range res {
			var n int
			s, n = pregReplaceCallbackAll(ctx, re, cb, s, limit)
			if ctx.HasThrown() {
				return *new(string)
			}
			total += int64(n)
		}
		return s
	}
	var result vm.Value
	if subjArr {
		out := vm.NewArray()
		for k, v := range subjSlot.AsArray().All() {
			out.Set(k, vm.MakeString(replaceOne(v.ToString())))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		result = vm.MakeArray(out)
	} else {
		result = vm.MakeString(replaceOne(subjSlot.ToString()))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	pregWriteRef(ctx, 4, vm.MakeInt(total))
	return result
}

// pregSplitFn реализует preg_split.
func pregSplitFn(ctx *vm.Context) vm.Value {
	pregLastError = pregErrNoError
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("preg_split() expects at least 2 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	re, utf8mode, err := pregCompile(arg(ctx, 0).ToString())
	if err != nil {
		pregLastError = pregErrInternal
		return vm.Bool(false)
	}
	subject := arg(ctx, 1).ToString()
	limit := int64(-1)
	if len(args) >= 3 {
		limit = arg(ctx, 2).ToInt()
	}
	flags := int64(0)
	if len(args) >= 4 {
		flags = arg(ctx, 3).ToInt()
	}
	if utf8mode && !utf8.ValidString(subject) {
		pregLastError = pregErrBadUTF8
		return vm.Bool(false)
	}
	type piece struct {
		s   string
		off int
	}
	var pieces []piece
	ms := re.FindAllStringSubmatchIndex(subject, -1)
	prev := 0
	splits := 0
	for _, m := range ms {
		if limit > 0 && int64(splits) >= limit-1 {
			break
		}
		pieces = append(pieces, piece{subject[prev:m[0]], prev})
		if flags&pregSplitDelimCapture != 0 {
			for g := 1; g < len(m)/2; g++ {
				if m[2*g] >= 0 {
					pieces = append(pieces, piece{subject[m[2*g]:m[2*g+1]], m[2*g]})
				}
			}
		}
		prev = m[1]
		splits++
	}
	pieces = append(pieces, piece{subject[prev:], prev})
	out := vm.NewArray()
	for _, p := range pieces {
		if flags&pregSplitNoEmpty != 0 && p.s == "" {
			continue
		}
		if flags&pregSplitOffsetCapture != 0 {
			pair := vm.NewArray()
			pair.Append(vm.MakeString(p.s))
			pair.Append(vm.MakeInt(int64(p.off)))
			out.Append(vm.MakeArray(pair))
		} else {
			out.Append(vm.MakeString(p.s))
		}
	}
	return vm.MakeArray(out)
}

// pregGrepFn реализует preg_grep.
func pregGrepFn(ctx *vm.Context) vm.Value {
	pregLastError = pregErrNoError
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("preg_grep() expects at least 2 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	re, _, err := pregCompile(arg(ctx, 0).ToString())
	if err != nil {
		pregLastError = pregErrInternal
		return vm.Bool(false)
	}
	arr := requireArrayArg(ctx, 1, "preg_grep")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	flags := int64(0)
	if len(args) >= 3 {
		flags = arg(ctx, 2).ToInt()
	}
	invert := flags&pregGrepInvert != 0
	out := vm.NewArray()
	for k, v := range arr.All() {
		if re.MatchString(v.ToString()) != invert {
			out.Set(k, v)
		}
	}
	return vm.MakeArray(out)
}

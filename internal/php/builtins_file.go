package php

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"phpvm/internal/vm"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// errnoDesc повторяет хвост PHP-диагностики потока: "<errno> <Strerror>",
// напр. "9 Bad file descriptor". Для не-syscall ошибок возвращает текст.
func errnoDesc(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		s := errno.Error()
		if s != "" {
			s = strings.ToUpper(s[:1]) + s[1:]
		}
		return strconv.Itoa(int(errno)) + " " + s
	}
	return err.Error()
}

// Файл содержит встроенные функции файловой системы (fopen/чтение/запись,
// проверки пути, каталоги, fnmatch, CSV, parse_ini_file).

// Сканеры INI (INI_SCANNER_*): 0 — обычный, 1 — raw, 2 — typed.
const (
	iniScannerNormal = 0
	iniScannerRaw    = 1
	iniScannerTyped  = 2
)

// Виды токенов значений INI.
const (
	iniTString = iota
	iniTNumber
	iniTConstant
	iniTWhitespace
	iniTOp
	iniTBoolTrue
	iniTBoolFalse
	iniTNull
)

type iniTok struct {
	kind int
	text string
}

// iniParser реализует разбор INI по правилам PHP (zend_ini_scanner/parser).
type iniParser struct {
	ctx      *vm.Context
	sections bool
	scanner  int
	file     string
	line     int
	failed   bool
	errMsg   string
}

func parseIniFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	sections := false
	scanner := iniScannerNormal
	if len(ctx.Args()) >= 2 {
		sections = arg(ctx, 1).ToBool()
	}
	if len(ctx.Args()) >= 3 {
		scanner = int(arg(ctx, 2).ToInt())
	}
	return iniParseString(ctx, s, sections, scanner, "Unknown")
}

// iniParseString разбирает INI-строку. При синтаксической ошибке возвращает
// false (как PHP) и печатает Warning.
func iniParseString(ctx *vm.Context, s string, sections bool, scanner int, file string) vm.Value {
	// Zend (zend_parse_ini_file): недопустимый режим сканера — Warning и false.
	if scanner < iniScannerNormal || scanner > iniScannerTyped {
		ctx.VM().Warning("Invalid scanner mode")
		return vm.Bool(false)
	}
	p := &iniParser{ctx: ctx, sections: sections, scanner: scanner, file: file, line: 1}
	out := p.parse(s)
	if p.failed {
		ctx.VM().Warn(p.errMsg)
		return vm.Bool(false)
	}
	return vm.MakeArray(out)
}

func (p *iniParser) fail(msg string) {
	if !p.failed {
		p.failed = true
		p.errMsg = msg
	}
}

func (p *iniParser) syntaxError(tok string) {
	p.fail("syntax error, unexpected '" + tok + "' in " + p.file + " on line " + strconv.Itoa(p.line) + "\n")
}

func (p *iniParser) parse(s string) *vm.Array {
	out := vm.NewArray()
	var cur *vm.Array
	i, n := 0, len(s)
	for i < n {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		c := s[i]
		switch {
		case c == '\n':
			i++
			p.line++
			continue
		case c == '\r':
			i++
			if i < n && s[i] == '\n' {
				i++
			}
			p.line++
			continue
		case c == ';':
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		case c == '[':
			name, ni := p.scanUntil(s, i+1, ']')
			i = ni
			if i < n && s[i] == ']' {
				i++
			} else {
				p.syntaxError("end of file")
			}
			if p.sections {
				cur = vm.NewArray()
				iniArrSet(out, name, vm.MakeArray(cur))
			}
			i = p.skipLine(s, i)
			continue
		}
		// метка
		start := i
		for i < n && iniLabelChar(s[i]) {
			i++
		}
		label := strings.TrimSpace(s[start:i])
		j := i
		for j < n && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j < n && s[j] == '[' {
			off, oi := p.scanUntil(s, j+1, ']')
			i = oi
			if i < n && s[i] == ']' {
				i++
			} else {
				p.syntaxError("end of file")
			}
			for i < n && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			if i < n && s[i] == '=' {
				i++
				for i < n && (s[i] == ' ' || s[i] == '\t') {
					i++
				}
				val, ni := p.evalValue(s, i)
				i = ni
				target := out
				if p.sections && cur != nil {
					target = cur
				}
				storeIniOffset(target, label, off, val)
			} else {
				p.syntaxError("end of file")
			}
			i = p.skipLine(s, i)
			continue
		}
		if j < n && s[j] == '=' {
			i = j + 1
			for i < n && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			val, ni := p.evalValue(s, i)
			i = ni
			target := out
			if p.sections && cur != nil {
				target = cur
			}
			iniArrSet(target, label, val)
			i = p.skipLine(s, i)
			continue
		}
		// голая метка — ничего не делаем
		i = p.skipLine(s, j)
	}
	return out
}

// skipLine проматывает до конца строки.
func (p *iniParser) skipLine(s string, i int) int {
	for i < len(s) && s[i] != '\n' && s[i] != '\r' {
		i++
	}
	return i
}

// scanUntil читает section/offset-значение до символа stop (или EOL).
// Ведущие и хвостовые пробелы вне кавычек отбрасываются (как в сканере PHP).
func (p *iniParser) scanUntil(s string, i int, stop byte) (string, int) {
	var b strings.Builder
	lastKeep := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for i < len(s) {
		c := s[i]
		if c == stop || c == '\n' || c == '\r' {
			break
		}
		if c == '"' {
			content, ni := p.scanDoubleQuoted(s, i+1)
			b.WriteString(content)
			lastKeep = b.Len()
			i = ni
			continue
		}
		if c == '\'' {
			j := i + 1
			for j < len(s) && s[j] != '\'' {
				j++
			}
			if j < len(s) {
				b.WriteString(s[i+1 : j])
				i = j + 1
			} else {
				b.WriteString(s[i+1 : j])
				i = j
			}
			lastKeep = b.Len()
			continue
		}
		if c == '$' && i+1 < len(s) && s[i+1] == '{' {
			val, ni := p.scanVarRef(s, i+2)
			b.WriteString(val)
			lastKeep = b.Len()
			i = ni
			continue
		}
		b.WriteByte(c)
		if c != ' ' && c != '\t' {
			lastKeep = b.Len()
		}
		i++
	}
	out := b.String()
	if lastKeep < len(out) {
		out = out[:lastKeep]
	}
	return out, i
}

// scanDoubleQuoted читает содержимое двойной строки (i — после открывающей
// кавычки) с обработкой escape-последовательностей и подстановок переменных.
func (p *iniParser) scanDoubleQuoted(s string, i int) (string, int) {
	var b strings.Builder
	for i < len(s) {
		c := s[i]
		if c == '"' {
			return b.String(), i + 1
		}
		if c == '\\' && i+1 < len(s) {
			nxt := s[i+1]
			// Windows-путь: key="C:\path\" — \" в конце строки закрывает
			// кавычку, а слэш остаётся в значении.
			if nxt == '"' && (i+2 >= len(s) || s[i+2] == '\n' || s[i+2] == '\r') {
				b.WriteByte('\\')
				return b.String(), i + 2
			}
			switch nxt {
			case '"', '\\', '$':
				b.WriteByte(nxt)
				i += 2
				continue
			default:
				b.WriteByte('\\')
				b.WriteByte(nxt)
				i += 2
				continue
			}
		}
		if c == '$' && i+1 < len(s) && s[i+1] == '{' {
			val, ni := p.scanVarRef(s, i+2)
			b.WriteString(val)
			i = ni
			continue
		}
		if c == '\n' {
			p.line++
		}
		b.WriteByte(c)
		i++
	}
	p.fail("syntax error, unexpected end of file, expecting TC_DOLLAR_CURLY or TC_QUOTED_STRING or '\"' in " + p.file + " on line " + strconv.Itoa(p.line) + "\n")
	return b.String(), i
}

// scanVarRef читает ${...} (i — после "${") и возвращает значение.
func (p *iniParser) scanVarRef(s string, i int) (string, int) {
	start := i
	for i < len(s) {
		c := s[i]
		if c == ':' && i+1 < len(s) && s[i+1] == '-' {
			break
		}
		if c == ':' {
			i++
			continue
		}
		if strings.IndexByte("= \n\r\t;&|^$~(){}!\"[]", c) >= 0 {
			break
		}
		i++
	}
	name := strings.TrimSpace(s[start:i])
	var fb string
	hasFb := false
	if i+1 < len(s) && s[i] == ':' && s[i+1] == '-' {
		i += 2
		fb, i = p.scanFallback(s, i)
		hasFb = true
	}
	if i < len(s) && s[i] == '}' {
		i++
	} else if hasFb {
		p.fail("syntax error, unexpected TC_FALLBACK, expecting TC_VARNAME in " + p.file + " on line " + strconv.Itoa(p.line) + "\n")
	} else {
		p.fail("syntax error, unexpected end of file, expecting TC_FALLBACK or '}' in " + p.file + " on line " + strconv.Itoa(p.line) + "\n")
	}
	if v, ok := p.iniSetting(name); ok {
		return v, i
	}
	if v, ok := os.LookupEnv(name); ok {
		return v, i
	}
	if v, ok := p.envValue(name); ok {
		return v, i
	}
	if hasFb {
		return fb, i
	}
	return "", i
}

// envValue ищет переменную окружения в $_ENV (эмуляция zend_getenv для
// прогонов, где ENV-переменные заданы через секцию --ENV--).
func (p *iniParser) envValue(name string) (string, bool) {
	env, ok := p.ctx.VM().GlobalGet("_ENV")
	if !ok || env.Type() != vm.ArrayType {
		return "", false
	}
	a := env.AsArray()
	if a == nil {
		return "", false
	}
	if v, ok := a.Get(vm.MakeStrKey(name)); ok {
		return v.ToString(), true
	}
	return "", false
}

// scanFallback читает fallback-значение ${var:-fallback} до '}'.
func (p *iniParser) scanFallback(s string, i int) (string, int) {
	var buf []byte
	lastKeep := 0
	n := len(s)
	for i < n {
		c := s[i]
		if c == '}' || c == '\n' || c == '\r' {
			break
		}
		if c == '"' {
			buf = buf[:lastKeep]
			content, ni := p.scanDoubleQuoted(s, i+1)
			buf = append(buf, content...)
			lastKeep = len(buf)
			i = ni
			for i < n && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			continue
		}
		if c == '\'' {
			j := i + 1
			for j < n && s[j] != '\'' {
				j++
			}
			buf = append(buf, s[i+1:j]...)
			lastKeep = len(buf)
			if j < n {
				i = j + 1
			} else {
				i = j
			}
			continue
		}
		if c == '$' && i+1 < n && s[i+1] == '{' {
			val, ni := p.scanVarRef(s, i+2)
			buf = append(buf, val...)
			lastKeep = len(buf)
			i = ni
			continue
		}
		if isIniConstStart(c) {
			j := i + 1
			for j < n && isIniConstChar(s[j]) {
				j++
			}
			buf = append(buf, p.expandConstant(s[i:j])...)
			lastKeep = len(buf)
			i = j
			continue
		}
		buf = append(buf, c)
		if c != ' ' && c != '\t' {
			lastKeep = len(buf)
		}
		i++
	}
	return string(buf), i
}

// iniSetting ищет значение настройки php.ini (zend_get_configuration_directive).
func (p *iniParser) iniSetting(name string) (string, bool) {
	if v, ok := iniValues[normalizeIniKey(name)]; ok {
		return v, true
	}
	return "", false
}

// expandConstant повторяет zend_ini_get_constant.
func (p *iniParser) expandConstant(name string) string {
	if strings.ContainsRune(name, ':') {
		return name
	}
	switch name {
	case "true":
		return "1"
	case "false", "null":
		return ""
	}
	if v, ok := p.ctx.VM().Const(name); ok {
		return v.ToString()
	}
	if v, ok := stdConst(name); ok {
		return v.ToString()
	}
	return name
}

// iniRawValue реализует правило ST_RAW сканера zend_ini (INI_SCANNER_RAW):
// значение берётся «как есть»; ';' вне ведущей кавычки обрывает значение;
// если значение целиком в двойных кавычках, они снимаются.
func (p *iniParser) iniRawValue(s string, i int) (string, int) {
	// re2c: RAW_VALUE_CHARS+ — до ';', '\n', '\r' или NUL.
	j := i
	for j < len(s) && s[j] != '\n' && s[j] != '\r' && s[j] != ';' && s[j] != 0 {
		j++
	}
	sc := -1
	if j < len(s) && s[j] == ';' {
		k := j
		for k < len(s) {
			c := s[k]
			if c == '\n' || c == '\r' {
				break
			}
			switch {
			case c == ';':
				if sc < 0 {
					sc = k
				}
				k++
			case c == '"':
				if s[i] == '"' {
					sc = -1
				}
				k++
			default:
				k++
			}
		}
		j = k
	}
	end := j
	if sc >= 0 {
		end = sc
	}
	val := s[i:end]
	val = strings.TrimRight(val, " \t")
	if len(val) > 0 && val[0] == '"' && !(len(val) > 1 && val[len(val)-1] == '"') {
		// RAW-сканер: незакрытая кавычка в строке — syntax error (Zend:
		// unexpected '"'); значение с переносом строки не допускается.
		p.syntaxError(`"`)
		return val, j
	}
	if len(val) > 1 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	return val, j
}

// evalValue разбирает значение от позиции i и возвращает слот и новый индекс.
func (p *iniParser) evalValue(s string, i int) (vm.Slot, int) {
	if p.scanner == iniScannerRaw {
		v, ni := p.iniRawValue(s, i)
		return vm.MakeString(v), ni
	}
	toks, ni := p.tokenizeValue(s, i)
	if len(toks) == 0 {
		return vm.MakeString(""), ni
	}
	if len(toks) == 1 {
		switch toks[0].kind {
		case iniTBoolTrue:
			if p.scanner == iniScannerTyped {
				return vm.MakeBool(true), ni
			}
			return vm.MakeString("1"), ni
		case iniTBoolFalse:
			if p.scanner == iniScannerTyped {
				return vm.MakeBool(false), ni
			}
			return vm.MakeString(""), ni
		case iniTNull:
			if p.scanner == iniScannerTyped {
				return vm.MakeNull(), ni
			}
			return vm.MakeString(""), ni
		}
	}
	ep := &iniExprParser{p: p, toks: toks}
	slot, single, hadOp := ep.parse()
	if p.failed {
		return vm.MakeString(""), ni
	}
	if hadOp {
		if p.scanner == iniScannerTyped {
			return slot, ni
		}
		return vm.MakeString(slot.ToString()), ni
	}
	if single && p.scanner == iniScannerTyped {
		return iniConvertNumber(slot.ToString()), ni
	}
	return slot, ni
}

// iniConvertNumber повторяет convert_to_number (is_numeric_string).
func iniConvertNumber(s string) vm.Slot {
	if s == "" {
		return vm.MakeString(s)
	}
	hasDot := strings.ContainsRune(s, '.')
	hasDigit := false
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			hasDigit = true
		} else if s[i] != '.' && s[i] != '-' && s[i] != '+' {
			return vm.MakeString(s)
		}
	}
	if !hasDigit {
		return vm.MakeString(s)
	}
	if hasDot {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return vm.MakeFloat(f)
		}
		return vm.MakeString(s)
	}
	if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
		return vm.MakeInt(iv)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return vm.MakeFloat(f)
	}
	return vm.MakeString(s)
}

// tokenizeValue сканирует значение в токены до конца строки/комментария.
func (p *iniParser) tokenizeValue(s string, i int) ([]iniTok, int) {
	var toks []iniTok
	n := len(s)
	for i < n {
		c := s[i]
		if c == '\n' || c == '\r' || c == ';' {
			break
		}
		if c == '=' {
			// второй '=' в значении — синтаксическая ошибка.
			p.syntaxError("=")
			break
		}
		if c == '"' {
			content, ni := p.scanDoubleQuoted(s, i+1)
			toks = append(toks, iniTok{iniTString, content})
			i = ni
			// закрывающая кавычка в PHP «съедает» следующие пробелы
			for i < n && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			continue
		}
		if c == '\'' {
			j := i + 1
			for j < n && s[j] != '\'' {
				j++
			}
			toks = append(toks, iniTok{iniTString, s[i+1 : j]})
			if j < n {
				i = j + 1
			} else {
				i = j
			}
			continue
		}
		if c == '$' && i+1 < n && s[i+1] == '{' {
			val, ni := p.scanVarRef(s, i+2)
			toks = append(toks, iniTok{iniTString, val})
			i = ni
			continue
		}
		if c == ' ' || c == '\t' {
			j := i
			for j < n && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			if j >= n {
				// Хвостовые пробелы в конце строки (без перевода строки)
				// остаются частью значения (см. bug51899).
				toks = append(toks, iniTok{iniTWhitespace, s[i:j]})
				i = j
				break
			}
			if s[j] == '\n' || s[j] == '\r' || s[j] == ';' {
				i = j
				break
			}
			if s[j] == '"' {
				i = j
				continue
			}
			toks = append(toks, iniTok{iniTWhitespace, s[i:j]})
			i = j
			continue
		}
		if strings.IndexByte("&|^~()!", c) >= 0 {
			toks = append(toks, iniTok{iniTOp, string(c)})
			i++
			for i < n && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			continue
		}
		if k, kl := p.matchKeyword(s, i); kl > 0 {
			toks = append(toks, iniTok{k, ""})
			i += kl
			continue
		}
		if nl := iniNumberLen(s, i); nl > 0 {
			toks = append(toks, iniTok{iniTNumber, s[i : i+nl]})
			i += nl
			continue
		}
		if isIniConstStart(c) {
			j := i + 1
			for j < n && isIniConstChar(s[j]) {
				j++
			}
			toks = append(toks, iniTok{iniTConstant, s[i:j]})
			i = j
			continue
		}
		if iniValueChar(c) || (c == '$' && !(i+1 < n && s[i+1] == '{')) {
			j := i
			for j < n && (iniValueChar(s[j]) || (s[j] == '$' && !(j+1 < n && s[j+1] == '{'))) {
				j++
			}
			toks = append(toks, iniTok{iniTString, s[i:j]})
			i = j
			continue
		}
		// Недопустимый символ.
		p.syntaxError(string(c))
		break
	}
	return toks, i
}

// matchKeyword ищет логическое слово/значение (true/on/yes/false/.../null).
func (p *iniParser) matchKeyword(s string, i int) (int, int) {
	type kw struct {
		word string
		kind int
	}
	kws := []kw{
		{"true", iniTBoolTrue}, {"on", iniTBoolTrue}, {"yes", iniTBoolTrue},
		{"false", iniTBoolFalse}, {"off", iniTBoolFalse}, {"no", iniTBoolFalse}, {"none", iniTBoolFalse},
		{"null", iniTNull},
	}
	run := 0
	n := len(s)
	for i+run < n && (iniValueChar(s[i+run]) || (s[i+run] == '$' && !(i+run+1 < n && s[i+run+1] == '{'))) {
		run++
	}
	best := 0
	bestKind := 0
	for _, k := range kws {
		l := len(k.word)
		if i+l > len(s) {
			continue
		}
		if !strings.EqualFold(s[i:i+l], k.word) {
			continue
		}
		total := l
		for i+total < len(s) && (s[i+total] == ' ' || s[i+total] == '\t') {
			total++
		}
		if total >= run && total > best {
			best = total
			bestKind = k.kind
		}
	}
	if best == 0 {
		return 0, 0
	}
	return bestKind, best
}

func iniLabelChar(c byte) bool {
	switch c {
	case '=', '\n', '\r', '\t', ';', '&', '|', '^', '$', '~', '(', ')', '{', '}', '!', '"', '[', ']', 0:
		return false
	}
	return true
}

func iniValueChar(c byte) bool {
	switch c {
	case '$', '=', ' ', '\t', '\n', '\r', ';', '&', '|', '^', '~', '(', ')', '!', '"', '\'', 0:
		return false
	}
	return true
}

func isIniConstStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIniConstChar(c byte) bool {
	return isIniConstStart(c) || (c >= '0' && c <= '9')
}

// iniNumberLen возвращает длину NUMBER-совпадения в позиции i (0 — нет).
func iniNumberLen(s string, i int) int {
	n := len(s)
	// LNUM: [-][0-9]+
	best := 0
	k := i
	if k < n && s[k] == '-' {
		k++
	}
	d := k
	for d < n && s[d] >= '0' && s[d] <= '9' {
		d++
	}
	if d > k {
		best = d - i
	}
	// DNUM
	if d2 := iniDNumLen(s, i); d2 > best {
		best = d2
	}
	return best
}

func iniDNumLen(s string, i int) int {
	n := len(s)
	j := i
	for j < n && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j >= n || s[j] != '.' {
		return 0
	}
	dot := j
	j++
	for j < n && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if dot == i && j == dot+1 {
		// "." без цифр с обеих сторон не DNUM
		return 0
	}
	if dot == i && j == dot+1 {
		return 0
	}
	return j - i
}

// iniExprParser — рекурсивный спуск по грамматике expr.
type iniExprParser struct {
	p    *iniParser
	toks []iniTok
	pos  int
}

func (ep *iniExprParser) parse() (vm.Slot, bool, bool) {
	return ep.expr(false)
}

func (ep *iniExprParser) expr(nested bool) (vm.Slot, bool, bool) {
	lhs, single, had := ep.primary()
	for ep.pos < len(ep.toks) && ep.toks[ep.pos].kind == iniTOp && ep.toks[ep.pos].text != ")" {
		op := ep.toks[ep.pos].text
		if op != "&" && op != "|" && op != "^" {
			break
		}
		ep.pos++
		rhs, _, _ := ep.primary()
		lhs = ep.p.doOp(op, lhs, rhs)
		had = true
		single = false
	}
	return lhs, single, had
}

func (ep *iniExprParser) primary() (vm.Slot, bool, bool) {
	if ep.pos < len(ep.toks) && ep.toks[ep.pos].kind == iniTOp {
		op := ep.toks[ep.pos].text
		if op == "~" || op == "!" {
			ep.pos++
			operand, _, _ := ep.primary()
			return ep.p.doUnary(op, operand), false, true
		}
		if op == "(" {
			ep.pos++
			v, single, had := ep.expr(true)
			if ep.pos < len(ep.toks) && ep.toks[ep.pos].kind == iniTOp && ep.toks[ep.pos].text == ")" {
				ep.pos++
			}
			return v, single, had
		}
	}
	return ep.varStringList()
}

func (ep *iniExprParser) varStringList() (vm.Slot, bool, bool) {
	var b strings.Builder
	count := 0
	single := false
	for ep.pos < len(ep.toks) && ep.toks[ep.pos].kind != iniTOp {
		t := ep.toks[ep.pos]
		ep.pos++
		switch t.kind {
		case iniTConstant:
			b.WriteString(ep.p.expandConstant(t.text))
		case iniTBoolTrue:
			b.WriteString("1")
		case iniTBoolFalse, iniTNull:
			// пусто
		default:
			b.WriteString(t.text)
		}
		count++
		single = count == 1 && t.kind == iniTNumber
	}
	if count == 0 {
		return vm.MakeString(""), false, false
	}
	return vm.MakeString(b.String()), single, false
}

// iniAtoi повторяет C atoi (32-бит).
func iniAtoi(s string) int32 {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	var v int64
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		v = v*10 + int64(s[i]-'0')
		i++
	}
	if neg {
		v = -v
	}
	return int32(v)
}

func (p *iniParser) doOp(op string, a, b vm.Slot) vm.Slot {
	ia := iniAtoi(a.ToString())
	ib := iniAtoi(b.ToString())
	var r int32
	switch op {
	case "|":
		r = ia | ib
	case "&":
		r = ia & ib
	case "^":
		r = ia ^ ib
	}
	return vm.MakeInt(int64(r))
}

func (p *iniParser) doUnary(op string, a vm.Slot) vm.Slot {
	ia := iniAtoi(a.ToString())
	var r int32
	switch op {
	case "~":
		r = ^ia
	case "!":
		if ia == 0 {
			r = 1
		}
	}
	return vm.MakeInt(int64(r))
}

// iniKeyFor преобразует метку в ключ массива (zend_symtable_update).
func iniKeyFor(label string) vm.ArrayKey {
	if n, ok := canonicalDecimalKey(label); ok {
		return vm.MakeIntKey(n)
	}
	return vm.MakeStrKey(label)
}

// storeIniOffset сохраняет запись вида key[offset]=value (ZEND_INI_PARSER_POP_ENTRY).
func storeIniOffset(target *vm.Array, label, off string, val vm.Slot) {
	key := iniKeyFor(label)
	var sub *vm.Array
	if v, ok := target.Get(key); ok && v.Type() == vm.ArrayType {
		sub = v.AsArray()
	}
	if sub == nil {
		sub = vm.NewArray()
		_ = target.Set(key, vm.MakeArray(sub))
	}
	if off == "" {
		_, _ = sub.Append(val)
		return
	}
	iniArrSet(sub, off, val)
}

// canonicalDecimalKey повторяет правило PHP: каноническое десятичное целое
// (без ведущих нулей) — целочисленный ключ.
func canonicalDecimalKey(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	body := s
	neg := false
	if s[0] == '-' {
		neg = true
		body = s[1:]
	}
	if body == "" || (body[0] == '0' && len(body) > 1) {
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
	if neg && v == 0 {
		return 0, false
	}
	return v, true
}

// ============================================================================
// Файловые встроенные функции (fsAgent). Дескрипторы представляются как int:
// id слота в глобальной таблице fsHandles. Строковый путь и id-дескриптор
// различаются по типу слота (Int = дескриптор, String = путь).
// ============================================================================

type fsHandle struct {
	f      *os.File
	path   string
	mode   string
	eof    bool
	push   int // pushback-байт (-1 если пусто)
	tmp    bool
	dir    bool
	names  []string
	dirPos int
	// res — ресурс-обёртка (для установки Closed при fclose/closedir).
	res *vm.Resource
	// resSlot — слот ресурса-обёртки (для $this->stream у фильтров).
	resSlot vm.Slot
	// meta — дополнительные поля stream_get_meta_data (wrapper_type и пр.).
	meta map[string]vm.Slot
	// chunkSize — размер внутреннего буфера потока (по умолчанию 8192),
	// фигурирует в диагностиках чтения.
	chunkSize int
	// canRead/canWrite — права потока по режиму открытия (не все обёртки
	// поддерживают обе операции; проверяем до syscall, как PHP).
	canRead  bool
	canWrite bool
	// filterActive — сейчас выполняется callback пользовательского фильтра
	// этого потока: fclose($this->stream) изнутри не закрывает поток, а лишь
	// «гасит» ресурс (bug54350).
	filterActive bool
	// isMemory — поток php://memory/php://temp: чтение всегда доступно,
	// отказ записи не сопровождается Notice.
	isMemory bool
	// appendMode — режим 'a': каждая запись идёт в конец (для ин-мемори
	// буферов эмулируем вручную).
	appendMode bool
	// us — пользовательская обёртка потока (stream_wrapper_register). Если
	// задана, операции чтения/записи/позиционирования делегируются PHP-методам.
	us *userStream
	// filters — фильтры потока (stream_filter_append).
	filters []*streamFilter
	// filterBuf/filterEOF/rawConsumed — состояние читающих фильтров.
	filterBuf   []byte
	filterEOF   bool
	rawConsumed int64
	// --- сетевые потоки ---
	// conn — установленное соединение (клиент/принятый сокет);
	// ln — слушающий сокет (stream_socket_server).
	conn net.Conn
	ln   net.Listener
	// isSocket — поток является сокетом (влияет на stream_get_meta_data).
	isSocket bool
	// singleRead — чтение делает одну операцию ядра (pipe/socket): fread не
	// добивает запрошенный размер блокирующими чтениями, как PHP для
	// небуферизуемых потоков.
	singleRead bool
	// sockType — тип сокета для stream_type ("tcp_socket", "unix_socket").
	sockType string
	// timedOut — признак тайм-аута последней операции чтения/записи.
	timedOut bool
	// rbuf/rbof — буфер опережающего чтения и признак достижения конца.
	rbuf []byte
	rbof bool
	// shutRd/shutWr — shutdown() для сокетов.
	shutRd bool
	shutWr bool
	// blockMode — блокирующий режим (stream_set_blocking).
	blockMode bool
	// readTimeout/writeTimeout — тайм-ауты в наносекундах (0 — не задан).
	readTimeout  time.Duration
	writeTimeout time.Duration
	// uri — исходная строка, переданная в fopen (для stream_get_meta_data).
	uri string
	// outVM — VM для маршрутизации записи в php://stdout/php://stderr.
	outVM *vm.VM
}

// modeCaps разбирает режим потока и возвращает права чтения/записи.
func modeCaps(mode string) (canRead, canWrite bool) {
	base := byte('r')
	if mode != "" {
		base = mode[0]
	}
	plus := strings.Contains(mode, "+")
	writeChar := strings.ContainsAny(mode, "wa")
	switch base {
	case 'w', 'a', 'x', 'c':
		return plus || strings.Contains(mode, "r"), true
	default: // r (и неизвестные — как read-only)
		return true, plus || writeChar
	}
}

// ensureWrite проверяет право записи; при отказе печатает PHP-Notice и false.
func (h *fsHandle) ensureWrite(ctx *vm.Context, fn string, n int) bool {
	if h.canWrite || (h.us != nil && h.us.hasWrite) {
		return true
	}
	if h.isMemory {
		return false
	}
	ctx.VM().Notice(fmt.Sprintf("%s(): Write of %d bytes failed with errno=%s", fn, n, errnoDesc(syscall.EBADF)))
	return false
}

// ensureRead проверяет право чтения; при отказе печатает PHP-Notice.
func (h *fsHandle) ensureRead(ctx *vm.Context, fn string) bool {
	if h.canRead {
		return true
	}
	ctx.VM().Notice(fmt.Sprintf("%s(): Read of %d bytes failed with errno=%s", fn, h.chunk(), errnoDesc(syscall.EBADF)))
	return false
}

// chunk возвращает размер внутреннего буфера потока (8192 по умолчанию).
func (h *fsHandle) chunk() int {
	if h.chunkSize > 0 {
		return h.chunkSize
	}
	return 8192
}

var (
	fsHandles           = map[int64]*fsHandle{}
	fsNextID      int64 = 1
	fsIncludePath       = "."
	// fsLastDir — последний открытый каталог (аргумент по умолчанию для
	// закрывающих/читающих функций без параметра).
	fsLastDir *fsHandle
)

// closeAllStreams завершает запрос: Zend уничтожает список ресурсов после
// деструкторов — открытые потоки закрываются, user-фильтры получают
// filter($closing=true) и onClose (bug54350, ord1). Ресурсы-обёртки гасятся.
func closeAllStreams() {
	for _, h := range fsHandles {
		if h == nil {
			continue
		}
		// Ресурс гаснет до завершающего filter(closing=true): внутри вызова
		// is_resource($this->stream) уже false (gh20370).
		if h.res != nil {
			h.res.Closed = true
		}
		closeFiltersRequestEnd(h)
		h.Close()
		if h.tmp {
			os.Remove(h.path)
		}
		if h.res != nil {
			h.res.Closed = true
		}
	}
	fsHandles = map[int64]*fsHandle{}
	fsLastDir = nil
	for _, ph := range procHandles {
		if ph != nil {
			ph.finish(false)
		}
	}
}

func init() {
	vm.SetRequestEndHook(func(*vm.VM) { closeAllStreams() })
}

// ResetResourceState сбрасывает счётчик id ресурсов и таблицу дескрипторов.
// Вызывается тест-раннером на границе прогонов .phpt (аналог нового процесса).
func ResetResourceState() {
	// Незавершённые процессы предыдущего прогона гасятся (в PHP каждый тест —
	// новый процесс; здесь процессы не должны переживать тест и течь fd).
	for _, ph := range procHandles {
		if ph != nil {
			ph.killOrphan()
		}
	}
	// PHP CLI: STDIN/STDOUT/STDERR занимают 1..3, а контекст потока по
	// умолчанию (создаётся лениво при первой обёрточной операции) — 4.
	// Присваиваем его лениво (ensureDefaultStreamContext), поэтому первый
	// пользовательский ресурс proc_open-пайпа (контекст не нужен) получает 4,
	// как в PHP.
	fsNextID = 4
	fsDefaultCtx = false
	fsHandles = map[int64]*fsHandle{}
	fsLastDir = nil
	stdStreamRes = nil
	streamContextDefault = vm.Slot{}
	persistentStreams = map[string]vm.Slot{}
	disabledWrappers = map[string]bool{}
	userStatCache = map[string]vm.Slot{}
	streamWrapperURL = map[string]bool{}
	streamWrappers = map[string]string{}
	// Реестр пользовательских stream-фильтров — глобальный на процесс PHP;
	// в php-src каждый тест запускается новым процессом, поэтому между
	// прогонами его нужно чистить (иначе повторная регистрация того же
	// фильтра возвращает false — read.phpt после bug35916).
	streamFilterRegistry = map[string]string{}
	phpStdinRaw = nil
	procHandles = map[int64]*procHandle{}
	resetCookieState()
}

// parsedDataURI — результат разбора data://-URI.
type parsedDataURI struct {
	data      []byte
	mediaType string
	params    [][2]string
	base64    bool
	reason    string
}

// parseDataURI разбирает data:[<mediatype>][;param=value][;base64],<data>
// и повторяет диагностики PHP-обёртки RFC2397.
func parseDataURI(s string) parsedDataURI {
	var r parsedDataURI
	rest := s[len("data:"):]
	// data:// — authority-компонент (пустой) отбрасывается.
	rest = strings.TrimPrefix(rest, "//")
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		r.reason = "rfc2397: no comma in URL"
		return r
	}
	meta := rest[:comma]
	payload := rest[comma+1:]
	parts := strings.Split(meta, ";")
	mt := parts[0]
	if mt != "" {
		if strings.IndexByte(mt, '=') >= 0 || strings.IndexByte(mt, '/') < 0 {
			r.reason = "rfc2397: illegal media type"
			return r
		}
		r.mediaType = mt
	}
	for _, p := range parts[1:] {
		if p == "base64" {
			r.base64 = true
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			r.reason = "rfc2397: illegal parameter"
			return r
		}
		r.params = append(r.params, [2]string{p[:eq], p[eq+1:]})
	}
	// Параметры без media type недопустимы (кроме ;base64).
	if r.mediaType == "" && len(r.params) > 0 {
		r.reason = "rfc2397: illegal media type"
		return r
	}
	if r.base64 {
		d, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			r.reason = "rfc2397: unable to decode"
			return r
		}
		r.data = d
		return r
	}
	var out []byte
	for i := 0; i < len(payload); i++ {
		if payload[i] == '%' && i+2 < len(payload) {
			h, err1 := strconv.ParseUint(payload[i+1:i+3], 16, 8)
			if err1 == nil {
				out = append(out, byte(h))
				i += 2
				continue
			}
		}
		out = append(out, payload[i])
	}
	r.data = out
	return r
}

// decodeDataURI декодирует data://-URI (совместимая обёртка).
func decodeDataURI(s string) ([]byte, bool) {
	r := parseDataURI(s)
	if r.reason != "" {
		return nil, false
	}
	return r.data, true
}

// openFailReason возвращает причину неудачи открытия с учётом data://-обёртки.
// phpFdError разбирает ошибки открытия php://fd-обёрток. Возвращает причину
// ошибки, признак необходимости дополнительного Warning «Invalid php:// URL»
// (для «php://fd» без номера) и признак, что путь вообще относится к php://fd.
func phpFdError(name string) (reason string, extra bool, handled bool) {
	if name == "php://fd" {
		return "operation failed", true, true
	}
	if !strings.HasPrefix(name, "php://fd/") {
		return "", false, false
	}
	rest := name[len("php://fd/"):]
	fd, err := strconv.Atoi(rest)
	if err != nil {
		return "php://fd/ stream must be specified in the form php://fd/<orig fd>", false, true
	}
	if fd < 0 || fd >= 524288 {
		return "The file descriptors must be non-negative numbers smaller than 524288", false, true
	}
	return "", false, false
}

func openFailReason(path, mode string) string {
	if lastUserOpenErr != "" {
		r := lastUserOpenErr
		lastUserOpenErr = ""
		return r
	}
	if strings.HasPrefix(path, "data:") {
		if r := parseDataURI(path); r.reason != "" {
			return r.reason
		}
	}
	return fileOpenReason(path, mode)
}

// readPathBytes читает локальный файл или data://-URI. Необязательный ctx
// нужен php://filter с пользовательскими фильтрами (диагностики и ошибки
// создания объекта фильтра, object_init_failure).
func readPathBytes(path string, ctxs ...*vm.Context) ([]byte, bool) {
	if strings.HasPrefix(path, "php://filter/") {
		return readFilterURL(path, ctxs...)
	}
	if strings.HasPrefix(path, "data:") {
		return decodeDataURI(path)
	}
	switch normalizePath(path) {
	case "php://input", "php://stdin":
		return []byte(phpRawInput), true
	case "php://memory", "php://temp":
		return []byte{}, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// filterPercentDecode декодирует %XX (для имён фильтров php://filter).
func filterPercentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi := qpHex(s[i+1])
			lo := qpHex(s[i+2])
			if hi >= 0 && lo >= 0 {
				b.WriteByte(byte(hi<<4 | lo))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// readFilterURL читает php://filter/<spec>/resource=<path> и применяет
// встроенные фильтры чтения.
func readFilterURL(path string, ctxs ...*vm.Context) ([]byte, bool) {
	rest := strings.TrimPrefix(path, "php://filter/")
	idx := strings.Index(rest, "/resource=")
	if idx < 0 {
		return nil, false
	}
	spec := rest[:idx]
	res := rest[idx+len("/resource="):]
	data, ok := readPathBytes(res, ctxs...)
	if !ok {
		return nil, false
	}
	chain := spec
	if i := strings.Index(spec, "="); i >= 0 {
		chain = spec[i+1:]
	}
	var ctx *vm.Context
	if len(ctxs) > 0 {
		ctx = ctxs[0]
	}
	for _, fn := range strings.Split(chain, "|") {
		name := filterPercentDecode(fn)
		if f, ok := builtinFilterFunc(name); ok {
			data = f(data)
			continue
		}
		cls, registered := streamFilterRegistry[name]
		if !registered || ctx == nil {
			continue
		}
		// Пользовательский фильтр: объект создаётся сразу; ошибка
		// (несуществующий класс, бросок в константе дефолта) даёт две
		// диагностики, а исходная ошибка всплывает наружу (object_init_failure).
		f := &streamFilter{name: name, userCls: cls, userVM: ctx.VMInstance(), userCtx: ctx, read: true}
		if !f.ensureInit() {
			// Диагностики Zend печатаются и при отложенной ошибке дефолта
			// (object_init_failure: предупреждения, затем Undefined constant).
			fnName := ctx.VM().CurrentFunctionName()
			ctx.VM().Warning(fnName + "(): Unable to create or locate filter \"" + name + "\"")
			ctx.VM().Warning(fnName + "(): Unable to create filter (" + name + ")")
			return nil, false
		}
		if out, code := f.applyUserCode(data, true); code == 0 {
			// PSFS_FEED_ME: выходная бригада не передаётся (oss_fuzz_385993744).
			data = nil
		} else {
			data = out
		}
	}
	return data, true
}

// readStreamBytes читает содержимое пути, поддерживая пользовательские
// обёртки (иначе — обычный файл или data://).
func readStreamBytes(ctx *vm.Context, path string) ([]byte, bool) {
	if h, ok := openUserStream(ctx, path, "r"); ok {
		d, _ := io.ReadAll(h)
		h.Close()
		return d, true
	}
	return readPathBytes(path, ctx)
}

// splitIncludePath разбивает include_path по PATH_SEPARATOR (":"), не разрывая
// схемы URL-обёрток вида "test://GLOBALS".
func splitIncludePath(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != ':' {
			continue
		}
		if i+2 < len(s) && s[i+1] == '/' && s[i+2] == '/' {
			continue
		}
		out = append(out, s[start:i])
		start = i + 1
	}
	out = append(out, s[start:])
	return out
}

// userPathExists проверяет существование пути пользовательской обёртки через
// url_stat (для поиска по include_path).
func userPathExists(ctx *vm.Context, path string) bool {
	scheme, ok := userScheme(path)
	if !ok {
		return false
	}
	if _, reg := streamWrappers[strings.ToLower(scheme)]; !reg {
		return false
	}
	res, handled := callUserWrapper(ctx, path, "url_stat", vm.MakeString(path), vm.MakeInt(0))
	if ctx.HasThrown() {
		return *new(bool)
	}
	return handled && res.Type() == vm.ArrayType
}

// fsResolveIncludePath ищет существующий файл по include_path (аналог
// use_include_path=1 в fopen/file_get_contents/file).
func fsResolveIncludePath(path string, ctxs ...*vm.Context) (string, bool) {
	for _, dir := range splitIncludePath(fsIncludePath) {
		if dir == "" {
			continue
		}
		if dir == "." {
			if _, err := os.Stat(path); err == nil {
				return path, true
			}
			continue
		}
		cand := dir
		if !strings.HasSuffix(cand, "/") {
			cand += "/"
		}
		cand += path
		if strings.Contains(cand, "://") {
			if len(ctxs) > 0 && ctxs[0] != nil && userPathExists(ctxs[0], cand) {
				return cand, true
			}
			continue
		}
		if _, err := os.Stat(cand); err == nil {
			return cand, true
		}
	}
	// Fallback php_resolve_path: каталог исполняемого скрипта идёт ПЕРЕД
	// literal-путём (текущим рабочим каталогом).
	if len(ctxs) > 0 && ctxs[0] != nil {
		if execFile := ctxs[0].VM().CurrentFile(); execFile != "" {
			dir := filepath.Dir(execFile)
			cand := filepath.Join(dir, path)
			if _, err := os.Stat(cand); err == nil {
				return cand, true
			}
		}
	}
	// Literal-путь (текущий рабочий каталог).
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	return path, false
}

// dupFile дублирует файловый дескриптор fd (1=stdout, 2=stderr), чтобы
// fclose дубликата не закрывал стандартный поток процесса.
func dupFile(fd int, name string) *os.File {
	nfd, err := syscall.Dup(fd)
	if err != nil {
		return os.Stdout
	}
	return os.NewFile(uintptr(nfd), name)
}

// fsNewResource создаёт ресурс-обёртку над дескриптором h и регистрирует его
// в таблице fsHandles (id -> handle). kind — "stream"/"directory".
func fsNewResource(h *fsHandle, kind string) vm.Slot {
	id := fsNextID
	fsNextID++
	s := vm.MakeResource(id, kind, h)
	h.res = vm.AsResource(s)
	h.resSlot = s
	fsHandles[id] = h
	return s
}

// fsNewHandle оставлен для внутренних нужд (id дескриптора без ресурса).
func fsNewHandle(h *fsHandle) int64 {
	id := fsNextID
	fsNextID++
	fsHandles[id] = h
	return id
}

// fsHandleOf возвращает дескриптор для аргумента: ресурс-поток/каталог либо
// (для обратной совместимости) целочисленный id. Закрытый ресурс даёт nil.
func fsHandleOf(s vm.Slot) *fsHandle {
	s = vm.Deref(s)
	switch s.Type() {
	case vm.IntType:
		return fsHandles[s.Int()]
	case vm.ResourceType:
		r := vm.AsResource(s)
		if r == nil || r.Closed {
			return nil
		}
		h, _ := r.Val.(*fsHandle)
		return h
	}
	return nil
}

// streamArg извлекает дескриптор потока из аргумента #i с PHP 8-диагностикой:
// закрытый/невалидный ресурс и значение неверного типа приводят к TypeError
// (а не к возврату false, как в PHP 7).
func streamArg(ctx *vm.Context, i int, fn string) *fsHandle {
	s := arg(ctx, i)
	if h := fsHandleOf(s); h != nil {
		return h
	}
	if s.Type() == vm.ResourceType {
		ctx.Throw(vm.NewThrowable("TypeError", fn+"(): supplied resource is not a valid stream resource"))
		if ctx.HasThrown() {
			return *new(*fsHandle)
		}
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		vm.ArgTypeError(fn, i+1, "stream", "resource", phpTypeName(ctx, i), "", 0)))
	if ctx.HasThrown() {
		return *new(*fsHandle)
	}
	return nil
}

// fsResourceClosed помечает ресурс-обёртку закрытым и убирает из реестра.
func fsResourceClosed(h *fsHandle) {
	if h == nil {
		return
	}
	if h.res != nil {
		h.res.Closed = true
	}
	for id, v := range fsHandles {
		if v == h {
			delete(fsHandles, id)
		}
	}
}

// normalizePath снимает схему file:// (локальный потоковый обёртка) и
// приводит «file:///abs» к «/abs».
func normalizePath(s string) string {
	if len(s) >= 7 && strings.EqualFold(s[:7], "file://") {
		s = s[7:]
		if s == "" {
			return "/"
		}
		if !strings.HasPrefix(s, "/") {
			s = "/" + s
		}
	}
	return s
}

// knownWrapperScheme — схемы, известные этой VM (не считая пользовательских
// из streamWrappers): для них файлового fallback нет.
func knownWrapperScheme(s string) bool {
	switch strings.ToLower(s) {
	case "file", "php", "glob", "data", "http", "https", "ftp", "ftps",
		"compress.zlib", "compress.bzip2", "zlib", "phar", "ssh2", "rar",
		"ogg", "expect", "zip":
		return true
	}
	return false
}

// unknownWrapperLocalPath повторяет файловый fallback PHP для неизвестной
// схемы: строка целиком трактуется как путь с лексической нормализацией
// (expand_filepath «..» может подниматься выше несуществующих компонентов).
func unknownWrapperLocalPath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	wd, err := os.Getwd()
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(wd, path))
}

// unknownWrapperFallback: для пути с неизвестной схемой печатает warning
// «Unable to find the wrapper» и возвращает локальный путь; ok=false, если
// схема известна/нет схемы.
func unknownWrapperFallback(ctx *vm.Context, fn, raw string) (string, bool) {
	scheme, has := userScheme(raw)
	if !has || knownWrapperScheme(scheme) {
		return raw, false
	}
	if _, reg := streamWrappers[strings.ToLower(scheme)]; reg {
		return raw, false
	}
	ctx.VM().Warning(fn + `(): Unable to find the wrapper "` + scheme +
		`" - did you forget to enable it when you configured PHP?`)
	return unknownWrapperLocalPath(raw), true
}

// --- enforcement open_basedir ---

// openBasedirRuntime хранит абсолютные разрешённые пути, вычисленные в момент
// ini_set('open_basedir', ...) (runtime). Если nil — open_basedir берётся из INI
// и разворачивается относительно текущего каталога на каждой проверке (как при
// старте: значение "." следует за chdir()). Это повторяет поведение PHP, где
// runtime-значения фиксируются, а стартовые пересчитываются.
var openBasedirRuntime []string

// openBasedirRaw возвращает «сырое» значение open_basedir (для ini_get и текста
// предупреждения).
func openBasedirRaw() string {
	v, _ := IniValue("open_basedir")
	return strings.TrimSpace(v)
}

// expandOpenBasedirPath приводит путь к абсолютному, лексически разрешая "."
// и ".." относительно текущего каталога (аналог virtual_file_ex/CWD_EXPAND),
// не разрешая символические ссылки.
func expandOpenBasedirPath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), true
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return filepath.Clean(filepath.Join(wd, path)), true
}

// splitOpenBasedirEntries разбивает значение по PATH_SEPARATOR (":" на unix).
func splitOpenBasedirEntries(raw string) []string {
	var out []string
	for _, e := range strings.Split(raw, ":") {
		e = strings.TrimSpace(e)
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// openBasedirAllowedEntries возвращает список абсолютных разрешённых путей.
func openBasedirAllowedEntries() []string {
	if openBasedirRuntime != nil {
		return openBasedirRuntime
	}
	raw := openBasedirRaw()
	if raw == "" {
		return nil
	}
	var ents []string
	for _, e := range splitOpenBasedirEntries(raw) {
		if ap, ok := expandOpenBasedirPath(normalizePath(e)); ok {
			ents = append(ents, ap)
		}
	}
	return ents
}

// pathWithinOpenBasedir сообщает, попадает ли уже развёрнутый путь в один из
// разрешённых (по префиксу с границей каталога).
func pathWithinOpenBasedir(expanded string, entries []string) bool {
	sep := string(os.PathSeparator)
	for _, ap := range entries {
		if expanded == ap {
			return true
		}
		prefix := ap
		if !strings.HasSuffix(prefix, sep) {
			prefix += sep
		}
		if strings.HasPrefix(expanded, prefix) {
			return true
		}
	}
	return false
}

// openBasedirAllows сообщает, разрешён ли доступ к path. Нелокальные обёртки
// (php://, data:, glob:// и т.п.) не проверяются.
func openBasedirAllows(path string) bool {
	if openBasedirRaw() == "" {
		return true
	}
	np := normalizePath(path)
	if strings.Contains(np, "://") {
		return true
	}
	exp, ok := expandOpenBasedirPath(np)
	if !ok {
		return true
	}
	return pathWithinOpenBasedir(exp, openBasedirAllowedEntries())
}

// checkOpenBasedir печатает предупреждение и возвращает false, если доступ к
// path запрещён open_basedir. fn — имя функции, display — путь в сообщении
// (пустая строка — использовать path). Возвращает true, если доступ разрешён.
func checkOpenBasedir(vmi *vm.VM, fn, path, display string) bool {
	if openBasedirRaw() == "" {
		return true
	}
	if openBasedirAllows(path) {
		return true
	}
	if display == "" {
		display = normalizePath(path)
	}
	vmi.Warning(fn + "(): open_basedir restriction in effect. File(" + display +
		") is not within the allowed path(s): (" + openBasedirRaw() + ")")
	return false
}

// iniSetOpenBasedir применяет ini_set('open_basedir', s). Возвращает false,
// если новое значение пытается расширить разрешённые пути (open_basedir можно
// только ужесточать). Повторяет логику OnUpdateBaseDir.
func iniSetOpenBasedir(s string) bool {
	cur := openBasedirAllowedEntries()
	newRaw := strings.TrimSpace(s)
	if newRaw == "" {
		iniValues["open_basedir"] = s
		openBasedirRuntime = nil
		return true
	}
	var resolved []string
	for _, e := range splitOpenBasedirEntries(newRaw) {
		ap, ok := expandOpenBasedirPath(normalizePath(e))
		if !ok {
			return false
		}
		if len(cur) > 0 && !pathWithinOpenBasedir(ap, cur) {
			return false
		}
		resolved = append(resolved, ap)
	}
	iniValues["open_basedir"] = s
	openBasedirRuntime = resolved
	return true
}

// errorLogPathAllowed проверяет путь назначения error_log с учётом того, что
// специальные значения (syslog/stdout/stderr и пустая строка) не являются
// файловыми путями и не проверяются.
func errorLogPathAllowed(s string) bool {
	if openBasedirRaw() == "" {
		return true
	}
	switch s {
	case "", "syslog", "stderr", "stdout":
		return true
	}
	return openBasedirAllows(s)
}

// pathString принимает либо строку-путь, либо id дескриптора.
func pathString(ctx *vm.Context, i int) string {
	s := arg(ctx, i)
	if h := fsHandleOf(s); h != nil {
		return h.path
	}
	return normalizePath(s.ToString())
}

func (h *fsHandle) checkEOF() bool {
	if h.push >= 0 {
		return false
	}
	cur, err := h.Seek(0, io.SeekCurrent)
	if err != nil {
		return h.eof
	}
	fi, err := h.Stat()
	if err != nil {
		return h.eof
	}
	return cur >= fi.Size()
}

func (h *fsHandle) readN(n int) ([]byte, error) {
	if n <= 0 {
		return []byte{}, nil
	}
	// Слушающий сокет (stream_socket_server) читать нельзя: PHP молча
	// возвращает false (без Notice).
	if h.ln != nil {
		return nil, errStreamReadFailed
	}
	// Для сокетов/пользовательских обёрток fread выполняет одну операцию
	// чтения и возвращает доступные данные (не блокируется до length).
	if h.buffered() {
		buf := make([]byte, n)
		m, err := h.Read(buf)
		if m > 0 {
			h.eof = false
		}
		if err == errStreamTimeout || err == errStreamReadFailed {
			return buf[:m], err
		}
		if err != nil && err != io.EOF {
			return buf[:m], err
		}
		return buf[:m], nil
	}
	buf := make([]byte, n)
	total := 0
	for total < n {
		m, err := h.Read(buf[total:])
		total += m
		if m > 0 {
			// Успешное чтение снимает признак EOF (как в PHP: feof становится
			// true только после попытки чтения за концом файла).
			h.eof = false
		}
		if err != nil || m == 0 {
			if err == errStreamTimeout {
				if total > 0 {
					return buf[:total], nil
				}
				return buf[:total], err
			}
			h.eof = true
			if err != nil && err != io.EOF {
				return buf[:total], err
			}
			break
		}
	}
	return buf[:total], nil
}

func (h *fsHandle) readByte() (byte, bool) {
	if h.push >= 0 {
		b := byte(h.push)
		h.push = -1
		h.eof = false
		return b, true
	}
	var b [1]byte
	n, err := h.Read(b[:])
	if n == 0 {
		_ = err
		h.eof = true
		return 0, false
	}
	h.eof = false
	return b[0], true
}

func (h *fsHandle) unreadByte(b byte) {
	h.push = int(b)
	// Байт прочитан и «возвращён»: позиция потока сдвинута за него, но для
	// логики feof он ещё не потреблён.
	h.eof = false
}

// --- fopen / fclose ---

func fopenFn(ctx *vm.Context) vm.Value {
	origName := arg0(ctx).ToString()
	name := pathString(ctx, 0)
	mode := "r"
	if len(ctx.Args()) >= 2 {
		mode = arg(ctx, 1).ToString()
	}
	if disabledWrappers["file"] && strings.HasPrefix(strings.ToLower(origName), "file://") {
		ctx.VM().Warning(`fopen(): Unable to find the wrapper "file" - did you forget to enable it when you configured PHP?`)
		ctx.VM().Warning("fopen(): file:// wrapper is disabled in the server configuration")
		ctx.VM().Warning(fmt.Sprintf("fopen(%s): Failed to open stream: no suitable wrapper could be found", origName))
		return vm.Bool(false)
	}
	if scheme, ok := userScheme(name); ok {
		if reason, blocked := urlWrapperBlocked(scheme); blocked {
			warnWrapperDisabled(ctx, "fopen", scheme, origName, reason)
			return vm.Bool(false)
		}
	}
	// Режим проверяется только для обычных файлов: php://-обёртки его
	// игнорируют (например, php://memory принимает любой режим).
	plainFile := !strings.Contains(name, "://")
	if plainFile && !validFopenMode(mode) {
		ctx.VM().Warning(fmt.Sprintf("fopen(%s): Failed to open stream: `%s' is not a valid mode for fopen", origName, mode))
		return vm.Bool(false)
	}
	if reason, extra, handled := phpFdError(name); handled {
		if extra {
			ctx.VM().Warning("fopen(): Invalid php:// URL specified")
		}
		ctx.VM().Warning(fmt.Sprintf("fopen(%s): Failed to open stream: %s", origName, reason))
		return vm.Bool(false)
	}
	if len(ctx.Args()) >= 3 && arg(ctx, 2).ToBool() {
		name, _ = fsResolveIncludePath(name, ctx)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if plainFile && !checkOpenBasedir(ctx.VM(), "fopen", name, origName) {
		ctx.VM().Warning(fmt.Sprintf("fopen(%s): Failed to open stream: %s", origName, "Operation not permitted"))
		return vm.Bool(false)
	}
	var h *fsHandle
	var ok bool
	if isHTTPURL(name) {
		h, ok = httpOpenStream(ctx, name, mode, 3, "fopen")
	} else {
		h, ok = fsOpen(ctx, name, mode)
	}
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !ok {
		ctx.VM().Warning(fmt.Sprintf("fopen(%s): Failed to open stream: %s", origName, openFailReason(name, mode)))
		return vm.Bool(false)
	}
	h.uri = origName
	return fsNewResource(h, "stream")
}

// validFopenMode проверяет допустимость режима fopen (r/w/a/x/c + опц. '+' и
// опц. 'b'/'t').
func validFopenMode(mode string) bool {
	if mode == "" {
		return true
	}
	switch mode[0] {
	case 'r', 'w', 'a', 'x', 'c':
	default:
		return false
	}
	plus := 0
	for _, ch := range mode[1:] {
		switch ch {
		case '+':
			plus++
			if plus > 1 {
				return false
			}
		case 'b', 't', 'e':
		default:
			return false
		}
	}
	return true
}

// fileOpenReason возвращает причину неудачи открытия (для Warning fopen).
func fileOpenReason(name, mode string) string {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return "No such file or directory"
		}
		return "Permission denied"
	}
	if mode != "" && mode[0] == 'x' {
		return "File exists"
	}
	return "Permission denied"
}

// openFlagsForMode переводит режим fopen в флаги os.OpenFile.
func openFlagsForMode(mode string) int {
	base := byte('r')
	if len(mode) > 0 {
		base = mode[0]
	}
	plus := strings.Contains(mode, "+")
	switch base {
	case 'r':
		if plus {
			return os.O_RDWR
		}
		return os.O_RDONLY
	case 'w':
		if plus {
			return os.O_RDWR | os.O_CREATE | os.O_TRUNC
		}
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case 'a':
		if plus {
			return os.O_RDWR | os.O_CREATE | os.O_APPEND
		}
		return os.O_WRONLY | os.O_CREATE | os.O_APPEND
	case 'x':
		if plus {
			return os.O_RDWR | os.O_CREATE | os.O_EXCL
		}
		return os.O_WRONLY | os.O_CREATE | os.O_EXCL
	case 'c':
		if plus {
			return os.O_RDWR | os.O_CREATE
		}
		return os.O_WRONLY | os.O_CREATE
	}
	return os.O_RDONLY
}

func fsOpen(ctx *vm.Context, name, mode string) (*fsHandle, bool) {
	lastUserOpenErr = ""
	// PHP: обёрточное открытие берёт контекст по умолчанию (ленивое создание
	// ресурса №4).
	ensureDefaultStreamContext()
	if mode == "" {
		mode = "r"
	}
	name = normalizePath(name)
	// compress.*:// обёртки в этой реализации прозрачны (данные хранятся
	// без фактического сжатия); согласованные пары фильтр/функция дают
	// корректный roundtrip.
	for _, pfx := range []string{"compress.bzip2://", "compress.zlib://", "compress.zstd://"} {
		if strings.HasPrefix(name, pfx) {
			name = name[len(pfx):]
			break
		}
	}
	canRead, canWrite := modeCaps(mode)
	if h, ok := openUserStream(ctx, name, mode); ok {
		return h, true
	}
	switch {
	case strings.HasPrefix(name, "php://temp"), strings.HasPrefix(name, "php://memory"):
		f, err := os.CreateTemp("", "php-temp")
		if err != nil {
			return nil, false
		}
		// Ин-мемори буфер: чтение всегда доступно, запись — по режиму.
		h := &fsHandle{f: f, path: name, mode: mode, push: -1, tmp: true, canRead: true, canWrite: canWrite, isMemory: true, appendMode: mode != "" && mode[0] == 'a'}
		h.meta = map[string]vm.Slot{"wrapper_type": vm.MakeString("PHP")}
		return h, true
	case name == "php://input" || name == "php://stdin":
		f, err := os.CreateTemp("", "php-input")
		if err != nil {
			return nil, false
		}
		if phpRawInput != "" {
			f.WriteString(phpRawInput)
			f.Seek(0, io.SeekStart)
		}
		return &fsHandle{f: f, path: name, mode: mode, push: -1, tmp: true, canRead: true}, true
	case name == "php://stdout":
		return &fsHandle{f: dupFile(1, name), path: name, mode: mode, push: -1, canRead: canRead, canWrite: true, outVM: ctx.VM()}, true
	case name == "php://stderr":
		return &fsHandle{f: dupFile(2, name), path: name, mode: mode, push: -1, canRead: canRead, canWrite: true, outVM: ctx.VM()}, true
	case name == "php://fd":
		lastUserOpenErr = "operation failed"
		return nil, false
	case strings.HasPrefix(name, "php://fd/"):
		if r, extra, handled := phpFdError(name); handled {
			_ = extra
			lastUserOpenErr = r
			return nil, false
		}
		fdStr := name[len("php://fd/"):]
		fd, perr := strconv.Atoi(fdStr)
		if perr != nil {
			return nil, false
		}
		// Дублируем fd: иначе закрытие/GC обёртки закроет реальный fd процесса
		// (1/2), ломая вывод харнесса.
		f := dupFile(fd, name)
		if f == nil {
			return nil, false
		}
		h := &fsHandle{f: f, path: name, mode: mode, push: -1, canRead: true, canWrite: true}
		if fd == 1 || fd == 2 {
			// Как php://stdout/php://stderr: встраиваем в буфер вывода VM,
			// иначе запись уйдёт в реальный fd харнесса и не попадёт в захват.
			h.outVM = ctx.VM()
		}
		return h, true
	case strings.HasPrefix(name, "data:"):
		data, ok := decodeDataURI(name)
		if !ok {
			return nil, false
		}
		f, err := os.CreateTemp("", "php-data")
		if err != nil {
			return nil, false
		}
		f.Write(data)
		f.Seek(0, io.SeekStart)
		h := &fsHandle{f: f, path: name, mode: mode, push: -1, tmp: true, canRead: true}
		h.meta = map[string]vm.Slot{"wrapper_type": vm.MakeString("RFC2397"), "stream_type": vm.MakeString("RFC2397")}
		return h, true
	}
	base := byte('r')
	if len(mode) > 0 {
		base = mode[0]
	}
	plus := strings.Contains(mode, "+")
	var flags int
	switch base {
	case 'r':
		if plus {
			flags = os.O_RDWR
		} else {
			flags = os.O_RDONLY
		}
	case 'w':
		if plus {
			flags = os.O_RDWR | os.O_CREATE | os.O_TRUNC
		} else {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
	case 'a':
		if plus {
			flags = os.O_RDWR | os.O_CREATE | os.O_APPEND
		} else {
			flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		}
	case 'x':
		if plus {
			flags = os.O_RDWR | os.O_CREATE | os.O_EXCL
		} else {
			flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
		}
	case 'c':
		if plus {
			flags = os.O_RDWR | os.O_CREATE
		} else {
			flags = os.O_WRONLY | os.O_CREATE
		}
	default:
		return nil, false
	}
	f, err := os.OpenFile(name, flags, 0666)
	if err != nil {
		return nil, false
	}
	// В 'a'/'a+' начальная позиция — 0 (как в PHP); записи всё равно идут в
	// конец благодаря O_APPEND (или appendMode для in-memory потоков).
	return &fsHandle{f: f, path: name, mode: mode, push: -1, canRead: canRead, canWrite: canWrite}, true
}

func fcloseFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx)
	h := fsHandleOf(s)
	if h == nil {
		if s.Type() == vm.ResourceType {
			ctx.Throw(vm.NewThrowable("TypeError", "fclose(): supplied resource is not a valid stream resource"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return vm.Bool(false)
	}
	if h.filterActive {
		// Zend: fclose изнутри callback фильтра недействителен — ресурс
		// становится «closed», поток продолжает жить (bug54350).
		id := int64(0)
		if r := vm.AsResource(s); r != nil {
			id = r.ID
		}
		ctx.VM().Warning(fmt.Sprintf("fclose(): %d is not a valid stream resource", id))
		// Ресурс «гасится» для userland, но сам поток остаётся в списке
		// ресурсов: при завершении запроса Zend закроет его, вызвав
		// filter(closing=true) (bug54350: второй вызов даёт TypeError).
		if h.res != nil {
			h.res.Closed = true
		}
		return vm.Bool(false)
	}
	closeFilters(h)
	h.Close()
	if h.tmp {
		os.Remove(h.path)
	}
	fsResourceClosed(h)
	return vm.Bool(true)
}

// --- чтение/запись ---

func freadFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fread")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !h.ensureRead(ctx, "fread") {
		return vm.Bool(false)
	}
	n := int(arg(ctx, 1).ToInt())
	if n <= 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "fread(): Argument #2 ($length) must be greater than 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	data, err := h.readN(n)
	if err == errStreamTimeout || err == errStreamReadFailed {
		return vm.Bool(false)
	}
	if err != nil {
		// UDP: неудачное чтение (ICMP/тайм-аут) — молчаливый false, как в PHP
		// (bug20134); notice «Read of ... failed» — для файловых потоков.
		if h.isSocket && h.sockType == "udp_socket" {
			return vm.Bool(false)
		}
		ctx.VM().Notice(fmt.Sprintf("fread(): Read of %d bytes failed with errno=%s", h.chunk(), errnoDesc(err)))
		return vm.Bool(false)
	}
	return vm.Str(string(data))
}

func fwriteFn(ctx *vm.Context) vm.Value {
	// PHP: при maxlen <= 0 запись не производится и возвращается 0 — проверка
	// ресурса при этом не срабатывает (даже для уже закрытого потока).
	if len(ctx.Args()) >= 3 && arg(ctx, 2).Type() != vm.NullType {
		l := int(arg(ctx, 2).ToInt())
		if l <= 0 {
			if arg0(ctx).Type() == vm.ResourceType {
				return vm.Int(0)
			}
		}
	}
	h := streamArg(ctx, 0, "fwrite")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	data := argToStringVal(ctx, 1)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if len(ctx.Args()) >= 3 {
		l := int(arg(ctx, 2).ToInt())
		if l > 0 && l < len(data) {
			data = data[:l]
		}
	}
	if !h.ensureWrite(ctx, "fwrite", len(data)) {
		return vm.Bool(false)
	}
	if h.appendMode {
		h.Seek(0, io.SeekEnd)
	}
	n, err := h.Write([]byte(data))
	if err == errStreamWriteFailed {
		return vm.Bool(false)
	}
	if err != nil {
		ctx.VM().Notice(fmt.Sprintf("fwrite(): Write of %d bytes failed with errno=%s", len(data), errnoDesc(err)))
		return vm.Bool(false)
	}
	return vm.Int(int64(n))
}

func fgetsFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fgets")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !h.ensureRead(ctx, "fgets") {
		return vm.Bool(false)
	}
	limit := -1
	if len(ctx.Args()) >= 2 {
		l := int(arg(ctx, 1).ToInt())
		if l <= 0 {
			ctx.Throw(vm.NewThrowable("ValueError", "fgets(): Argument #2 ($length) must be greater than 0"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
		limit = l - 1
	}
	var out []byte
	autoDetect := autoDetectLineEndingsOn()
	for limit < 0 || len(out) < limit {
		b, ok := h.readByte()
		if !ok {
			break
		}
		out = append(out, b)
		if b == '\n' || (autoDetect && b == '\r') {
			break
		}
	}
	if len(out) == 0 {
		return vm.Bool(false)
	}
	return vm.Str(string(out))
}

func fgetcFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fgetc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !h.ensureRead(ctx, "fgetc") {
		return vm.Bool(false)
	}
	b, ok := h.readByte()
	if !ok {
		return vm.Bool(false)
	}
	return vm.Str(string([]byte{b}))
}

func feofFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "feof")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h == nil {
		return vm.Bool(false)
	}
	if h.buffered() {
		if len(h.rbuf) > 0 {
			return vm.Bool(false)
		}
		if h.us != nil && h.us.hasEof {
			return vm.Bool(h.us.eof())
		}
		return vm.Bool(h.rbof)
	}
	return vm.Bool(h.eof)
}

func fseekFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fseek")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	off := arg(ctx, 1).ToInt()
	whence := int(arg(ctx, 2).ToInt())
	if h.dir {
		// fseek() на каталоге в PHP всегда перезапускает обход с начала.
		_ = off
		_ = whence
		h.dirPos = 0
		h.eof = false
		return vm.Int(0)
	}
	// Потоки без поддержки позиционирования (pipe/socket: php://stdout и пр.).
	if h.us == nil && h.f != nil {
		if fi, err := h.f.Stat(); err == nil && !fi.Mode().IsRegular() {
			if _, err := h.f.Seek(0, io.SeekCurrent); err != nil {
				ctx.VM().Warning("fseek(): Stream does not support seeking")
				return vm.Int(-1)
			}
		}
	}
	if h.us != nil {
		return fseekUser(ctx, h, off, whence)
	}
	h.push = -1
	if _, err := h.Seek(off, whence); err != nil {
		return vm.Int(-1)
	}
	h.resetFilterState()
	h.eof = false
	return vm.Int(0)
}

// userLogicalPos — логическая позиция пользовательского потока с учётом
// буфера опережающего чтения и pushback-байта.
func (h *fsHandle) userLogicalPos() int64 {
	p, err := h.us.tell()
	if err != nil {
		return 0
	}
	p -= int64(len(h.rbuf))
	if h.push >= 0 {
		p--
	}
	return p
}

// fseekUser выполняет fseek для пользовательской обёртки, согласуя буфер
// опережающего чтения, чтобы ftell() совпадал с обычным файлом.
func fseekUser(ctx *vm.Context, h *fsHandle, off int64, whence int) vm.Value {
	cur := h.userLogicalPos()
	var target int64
	switch whence {
	case io.SeekStart:
		target = off
	case io.SeekCurrent:
		target = cur + off
	default: // io.SeekEnd
		h.rbuf = nil
		h.push = -1
		if _, err := h.us.seek(off, io.SeekEnd); err != nil {
			return vm.Int(-1)
		}
		h.eof = false
		return vm.Int(0)
	}
	if target >= cur && target <= cur+int64(len(h.rbuf)) {
		// Цель внутри буфера: сдвигаем буфер, обёртку не трогаем.
		h.rbuf = h.rbuf[target-cur:]
		h.push = -1
		h.eof = false
		return vm.Int(0)
	}
	h.rbuf = nil
	h.push = -1
	if _, err := h.us.seek(target, io.SeekStart); err != nil {
		return vm.Int(-1)
	}
	h.eof = false
	return vm.Int(0)
}

func ftellFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "ftell")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.dir {
		return vm.Int(int64(h.dirPos))
	}
	if h.us != nil {
		if _, err := h.us.tell(); err != nil {
			return vm.Bool(false)
		}
		return vm.Int(h.userLogicalPos())
	}
	if len(h.filters) > 0 {
		raw, err := h.f.Seek(0, io.SeekCurrent)
		if err != nil {
			return vm.Bool(false)
		}
		return vm.Int(raw - int64(len(h.filterBuf)))
	}
	pos, err := h.Seek(0, io.SeekCurrent)
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Int(pos)
}

func rewindFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "rewind")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.dir {
		h.dirPos = 0
		h.eof = false
		return vm.Bool(true)
	}
	h.push = -1
	h.Seek(0, io.SeekStart)
	h.resetFilterState()
	h.eof = false
	return vm.Bool(true)
}

func fflushFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fflush")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	h.Sync()
	return vm.Bool(true)
}

func fpassthruFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fpassthru")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !h.ensureRead(ctx, "fpassthru") {
		return vm.Int(0)
	}
	var data []byte
	if h.push >= 0 {
		data = append(data, byte(h.push))
		h.push = -1
	}
	rest, _ := io.ReadAll(h)
	data = append(data, rest...)
	h.eof = true
	ctx.Print(string(data))
	return vm.Int(int64(len(data)))
}

func ftruncateFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "ftruncate")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	size := arg(ctx, 1).ToInt()
	if size < 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "ftruncate(): Argument #2 ($size) must be greater than or equal to 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if h.us != nil {
		o := h.us.vm.SlotObject(h.us.obj)
		var ci vm.ClassInfo
		if o != nil {
			ci = h.us.vm.FindClass(o.ClassName())
		}
		if ci == nil || !hasMethod(ci, "stream_truncate") {
			ctx.VM().Warning("ftruncate(): Can't truncate this stream!")
			return vm.Bool(false)
		}
		res, ok := h.us.callMethod("stream_truncate", vm.MakeInt(size))
		if !ok {
			return vm.Bool(false)
		}
		if res.Type() != vm.BoolType {
			ctx.VM().Warning(fmt.Sprintf("ftruncate(): %s::stream_truncate did not return a boolean!", o.ClassName()))
			return vm.Bool(false)
		}
		return res
	}
	if err := h.Truncate(size); err != nil {
		return vm.Bool(false)
	}
	// Для in-memory потоков PHP (начиная с 7.3) переводит указатель на новую
	// длину; для обычных файлов позиция не меняется.
	if h.isMemory {
		h.Seek(size, io.SeekStart)
	}
	return vm.Bool(true)
}

func flockFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "flock")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	opSlot := arg(ctx, 1)
	var op int64
	switch opSlot.Type() {
	case vm.IntType:
		op = opSlot.ToInt()
	case vm.BoolType, vm.FloatType:
		op = opSlot.ToInt()
	case vm.StringType:
		s := opSlot.ToString()
		if !isNumericIntString(s) {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("flock", 2, "operation", "int", "string", "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
		op = opSlot.ToInt()
	default:
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("flock", 2, "operation", "int", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	switch op & 3 {
	case 1, 2, 3:
	default:
		ctx.Throw(vm.NewThrowable("ValueError",
			"flock(): Argument #2 ($operation) must be one of LOCK_SH, LOCK_EX, or LOCK_UN"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	setWould := func(v int64) {
		if len(ctx.Args()) >= 3 {
			if s := arg(ctx, 2); s.IsRef() && s.Ref() != nil {
				s.Ref().Set(vm.MakeInt(v))
			}
		}
	}
	setWould(0)
	if h.us != nil {
		o := h.us.vm.SlotObject(h.us.obj)
		var ci vm.ClassInfo
		if o != nil {
			ci = h.us.vm.FindClass(o.ClassName())
		}
		if ci == nil || !hasMethod(ci, "stream_lock") {
			name := ""
			if o != nil {
				name = o.ClassName()
			}
			ctx.VM().Warning(fmt.Sprintf("flock(): %s::stream_lock is not implemented!", name))
			return vm.Bool(false)
		}
		if _, ok := h.us.callMethod("stream_lock", vm.MakeInt(op)); !ok {
			return vm.Bool(false)
		}
		return vm.Bool(true)
	}
	if h.dir || h.f == nil {
		return vm.Bool(false)
	}
	mapped := 0
	switch op & 3 {
	case 1:
		mapped = syscall.LOCK_SH
	case 2:
		mapped = syscall.LOCK_EX
	case 3:
		mapped = syscall.LOCK_UN
	}
	if op&4 != 0 {
		mapped |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(h.f.Fd()), mapped); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			setWould(1)
		}
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// isNumericIntString сообщает, что строка приводится к int без TypeError
// (PHP 8: нечисловые строки недопустимы для int-параметра).
func isNumericIntString(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	_, err := strconv.ParseInt(t, 10, 64)
	if err == nil {
		return true
	}
	// допускаем ведущие/замыкающие пробелы и знак для float-подобных целых
	_, ferr := strconv.ParseFloat(t, 64)
	return ferr == nil
}

func tmpfileFn(ctx *vm.Context) vm.Value {
	f, err := os.CreateTemp("", "php-tmpfile")
	if err != nil {
		return vm.Bool(false)
	}
	h := &fsHandle{f: f, path: f.Name(), mode: "w+", push: -1, tmp: true, canRead: true, canWrite: true}
	return fsNewResource(h, "stream")
}

// --- форматирование ---

func fprintfFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 2 {
		ctx.Throw(vm.NewThrowable("TypeError",
			fmt.Sprintf("fprintf() expects at least 2 arguments, %d given", len(ctx.Args()))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	h := streamArg(ctx, 0, "fprintf")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	s := sprintfValue(ctx, ctx.Args()[1:])
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.appendMode {
		h.Seek(0, io.SeekEnd)
	}
	n, err := h.Write([]byte(s))
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Int(int64(n))
}

func vfprintfFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) != 3 {
		ctx.Throw(vm.NewThrowable("TypeError",
			fmt.Sprintf("vfprintf() expects exactly 3 arguments, %d given", len(ctx.Args()))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if arg(ctx, 1).Type() == vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("vfprintf", 2, "format", "string", "array", "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if arg(ctx, 2).Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("vfprintf", 3, "values", "array", phpTypeName(ctx, 2), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	h := streamArg(ctx, 0, "vfprintf")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	format := arg(ctx, 1).ToString()
	argsArr := arg(ctx, 2)
	all := []vm.Slot{vm.MakeString(format)}
	if argsArr.Type() == vm.ArrayType {
		all = append(all, argsArr.AsArray().Values()...)
	}
	s := sprintfValue(ctx, all)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	n, err := h.Write([]byte(s))
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Int(int64(n))
}

// --- fscanf (упрощённо) ---

func fscanfFnLegacy(ctx *vm.Context) vm.Value {
	h := fsHandleOf(arg0(ctx))
	if h == nil {
		if arg0(ctx).Type() == vm.ResourceType {
			ctx.Throw(vm.NewThrowable("TypeError",
				"fscanf(): supplied resource is not a valid File-Handle resource"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return vm.Bool(false)
	}
	if len(ctx.Args()) < 2 {
		return vm.Bool(false)
	}
	format := arg(ctx, 1).ToString()
	out := vm.NewArray()
	count := 0
	i := 0
	// firstNonSpace пропускает пробелы ввода и возвращает первый значимый байт
	// (ok=false — достигнут EOF, байт не прочитан).
	firstNonSpace := func() (byte, bool) {
		for {
			b, ok := h.readByte()
			if !ok {
				return 0, false
			}
			if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
				return b, true
			}
		}
	}
	// skipInputWS пропускает пробелы ввода, не съедая первый значимый байт.
	skipInputWS := func() {
		for {
			b, ok := h.readByte()
			if !ok {
				// Пропуск пробелов не «потребляет» EOF: PHP выставит feof лишь
				// на следующей попытке преобразования.
				h.eof = false
				return
			}
			if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
				h.unreadByte(b)
				return
			}
		}
	}
	consumeEOL := func() {
		for {
			b, ok := h.readByte()
			if !ok {
				return
			}
			if b == '\n' {
				return
			}
		}
	}
	// readNumeric читает токен первого байта first (уже прочитанного) по
	// правилам spec и доводит до width символов.
	readRest := func(first byte, allowed func(byte) bool, width int) []byte {
		tok := []byte{first}
		for width == 0 || len(tok) < width {
			b, ok := h.readByte()
			if !ok {
				break
			}
			if allowed(b) {
				tok = append(tok, b)
			} else {
				h.unreadByte(b)
				break
			}
		}
		return tok
	}
	isDigit := func(b byte) bool { return b >= '0' && b <= '9' }

	isEOF := false
	failed := false
	lastSuppress := false
	for i < len(format) {
		c := format[i]
		if c == ' ' || c == '\t' || c == '\n' {
			skipInputWS()
			i++
			continue
		}
		if c != '%' {
			b, ok := h.readByte()
			if !ok || b != c {
				if ok {
					h.unreadByte(b)
				}
				if !ok {
					isEOF = true
				} else {
					failed = true
				}
				break
			}
			i++
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			b, ok := h.readByte()
			if !ok {
				isEOF = true
				break
			}
			if b != '%' {
				h.unreadByte(b)
				failed = true
				break
			}
			i++
			continue
		}
		suppress := false
		if i < len(format) && format[i] == '*' {
			suppress = true
			i++
		}
		width := 0
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			width = width*10 + int(format[i]-'0')
			i++
		}
		for i < len(format) && isLengthMod(format[i]) {
			i++
		}
		if i >= len(format) {
			break
		}
		spec := format[i]
		i++
		lastSuppress = suppress

		appendVal := func(v vm.Slot) {
			if !suppress {
				out.Append(v)
				count++
			}
		}

		switch spec {
		case 'd', 'i', 'u', 'o', 'x', 'X', 'f', 'e', 'g', 'E', 'G':
			first, ok := firstNonSpace()
			if !ok {
				isEOF = true
				break
			}
			allowed := func(b byte) bool { return false }
			switch spec {
			case 'd', 'u':
				allowed = isDigit
			case 'o':
				allowed = func(b byte) bool { return b >= '0' && b <= '7' }
			case 'x', 'X':
				allowed = func(b byte) bool {
					return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') || b == 'x' || b == 'X'
				}
			case 'i':
				allowed = func(b byte) bool {
					return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') || b == 'x' || b == 'X'
				}
			default: // f,e,g,E,G
				allowed = func(b byte) bool {
					return isDigit(b) || b == '.' || b == 'e' || b == 'E' || b == '+' || b == '-'
				}
			}
			if spec != 'f' && spec != 'e' && spec != 'g' && spec != 'E' && spec != 'G' {
				if first != '-' && first != '+' && !isDigit(first) &&
					!((spec == 'x' || spec == 'X' || spec == 'i') && (first == 'x' || first == 'X')) {
					failed = true
					break
				}
			}
			tok := readRest(first, allowed, width)
			// Ширина обрезала числовое поле: PHP дочитывает остаток поля,
			// чтобы не перечитать то же число в следующем вызове.
			if width > 0 && len(tok) >= width {
				for {
					b, ok := h.readByte()
					if !ok {
						break
					}
					if allowed(b) {
						continue
					}
					h.unreadByte(b)
					break
				}
			}
			tokStr := string(tok)
			var val vm.Slot
			converted := true
			neg := strings.HasPrefix(tokStr, "-")
			body := trimSign(tokStr)
			switch spec {
			case 'd':
				v, err := strconv.ParseInt(tokStr, 10, 64)
				if err != nil {
					converted = false
				} else {
					val = vm.MakeInt(v)
				}
			case 'u':
				if body == "" {
					converted = false
					break
				}
				uv, uerr := strconv.ParseUint(body, 10, 64)
				if uerr != nil {
					converted = false
				} else if neg {
					val = vm.MakeInt(-int64(uv))
				} else {
					val = vm.MakeInt(int64(uv))
				}
			case 'o':
				if body == "" {
					converted = false
					break
				}
				v, err := strconv.ParseInt(body, 8, 64)
				if err != nil {
					converted = false
					break
				}
				if neg {
					v = -v
				}
				val = vm.MakeInt(v)
			case 'x', 'X':
				s := strings.TrimPrefix(strings.TrimPrefix(body, "0x"), "0X")
				if s == "" {
					converted = false
					break
				}
				v, err := strconv.ParseInt(s, 16, 64)
				if err != nil {
					converted = false
					break
				}
				if neg {
					v = -v
				}
				val = vm.MakeInt(v)
			case 'i':
				if body == "" {
					converted = false
					break
				}
				var v int64
				var err error
				if strings.HasPrefix(body, "0x") || strings.HasPrefix(body, "0X") {
					v, err = strconv.ParseInt(body[2:], 16, 64)
				} else if len(body) > 1 && body[0] == '0' {
					v, err = strconv.ParseInt(body[1:], 8, 64)
				} else {
					v, err = strconv.ParseInt(body, 10, 64)
				}
				if err != nil {
					converted = false
					break
				}
				if neg {
					v = -v
				}
				val = vm.MakeInt(v)
			default:
				fv, err := strconv.ParseFloat(tokStr, 64)
				if err != nil {
					converted = false
				} else {
					val = vm.MakeFloat(fv)
				}
			}
			if !converted {
				failed = true
				break
			}
			appendVal(val)
		case 's':
			first, ok := firstNonSpace()
			if !ok {
				isEOF = true
				break
			}
			tok := readRest(first, func(b byte) bool {
				return b != ' ' && b != '\t' && b != '\n' && b != '\r'
			}, width)
			appendVal(vm.MakeString(string(tok)))
		case 'c':
			n := width
			if n == 0 {
				n = 1
			}
			var tok []byte
			for len(tok) < n {
				b, ok := h.readByte()
				if !ok {
					break
				}
				tok = append(tok, b)
			}
			if len(tok) == 0 {
				isEOF = true
				break
			}
			appendVal(vm.MakeString(string(tok)))
		case 'n':
			pos, _ := h.Seek(0, io.SeekCurrent)
			appendVal(vm.MakeInt(pos))
		case '[':
			neg := false
			if i < len(format) && (format[i] == '^' || format[i] == '!') {
				neg = true
				i++
			}
			set := map[byte]bool{}
			for i < len(format) && format[i] != ']' {
				if i+2 < len(format) && format[i+1] == '-' && format[i+2] != ']' {
					for c := format[i]; c <= format[i+2]; c++ {
						set[c] = true
					}
					i += 3
				} else {
					set[format[i]] = true
					i++
				}
			}
			if i < len(format) {
				i++
			}
			// PHP (в отличие от libc) пропускает пробелы перед scanset.
			skipInputWS()
			b, ok := h.readByte()
			if !ok {
				isEOF = true
				break
			}
			if set[b] == neg {
				h.unreadByte(b)
				failed = true
				break
			}
			tok := readRest(b, func(ch byte) bool { return set[ch] != neg }, width)
			appendVal(vm.MakeString(string(tok)))
		default:
			// Недопустимый спецификатор: PHP бросает ValueError. На EOF —
			// просто конец потока (bool(false)); иначе съедаем строку, чтобы
			// не зациклиться.
			if _, ok := h.readByte(); !ok {
				isEOF = true
				break
			}
			consumeEOL()
			ctx.Throw(vm.NewThrowable("ValueError",
				fmt.Sprintf("Bad scan conversion character %q", string(spec))))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
		if isEOF || failed {
			break
		}
	}
	if failed {
		// Неудачное преобразование: NULL в результат, остаток строки съедается
		// (иначе while(!feof())-циклы в тестах зациклятся).
		if !lastSuppress {
			out.Append(vm.MakeNull())
		}
		consumeEOL()
	}
	if len(ctx.Args()) <= 2 {
		if out.Len() == 0 && (isEOF || failed) {
			return vm.Bool(false)
		}
		return vm.MakeArray(out)
	}
	return vm.Int(int64(count))
}

// trimSign снимает ведущий знак числа (для повторного парсинга).
func trimSign(s string) string {
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		return s[1:]
	}
	return s
}

// isLengthMod сообщает, что байт — модификатор длины scanf (h/l/L/q/j/z/t).
func isLengthMod(b byte) bool {
	switch b {
	case 'h', 'l', 'L', 'q', 'j', 'z', 't':
		return true
	}
	return false
}

// --- файловое содержимое ---

func fileGetContentsFn(ctx *vm.Context) vm.Value {
	raw0 := arg0(ctx).ToString()
	if strings.IndexByte(raw0, 0) >= 0 {
		ctx.Throw(vm.NewThrowable("ValueError",
			"file_get_contents(): Argument #1 ($filename) must not contain any null bytes"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if len(ctx.Args()) >= 5 && arg(ctx, 4).Type() != vm.NullType {
		if arg(ctx, 4).ToInt() < 0 {
			ctx.Throw(vm.NewThrowable("ValueError", "file_get_contents(): Argument #5 ($length) must be greater than or equal to 0"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
	}
	path := pathString(ctx, 0)
	if len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool() {
		path, _ = fsResolveIncludePath(path, ctx)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if !checkOpenBasedir(ctx.VM(), "file_get_contents", path, raw0) {
		ctx.VM().Warning("file_get_contents(" + raw0 + "): Failed to open stream: Operation not permitted")
		return vm.Bool(false)
	}
	if scheme, ok := userScheme(path); ok {
		if reason, blocked := urlWrapperBlocked(scheme); blocked {
			warnWrapperDisabled(ctx, "file_get_contents", scheme, raw0, reason)
			return vm.Bool(false)
		}
	}
	if p2, unknown := unknownWrapperFallback(ctx, "file_get_contents", path); unknown {
		path = p2
	}
	var data []byte
	if isHTTPURL(path) {
		if h, ok := httpOpenStream(ctx, path, "r", 2, "file_get_contents"); ok {
			d, _ := io.ReadAll(h)
			h.Close()
			data = d
		} else {
			ctx.VM().Warning("file_get_contents(" + raw0 + "): Failed to open stream: " + openFailReason(path, "r"))
			return vm.Bool(false)
		}
	} else if h, ok := openUserStream(ctx, path, "r"); ok {
		d, _ := io.ReadAll(h)
		h.Close()
		data = d
	} else {
		var ok bool
		data, ok = readPathBytes(path, ctx)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		if !ok {
			ctx.VM().Warning("file_get_contents(" + raw0 + "): Failed to open stream: " + openFailReason(path, "r"))
			return vm.Bool(false)
		}
	}
	if len(ctx.Args()) >= 4 {
		off := int(arg(ctx, 3).ToInt())
		if off < 0 {
			off = len(data) + off
			if off < 0 {
				off = 0
			}
		}
		if off > len(data) {
			off = len(data)
		}
		data = data[off:]
	}
	if len(ctx.Args()) >= 5 && arg(ctx, 4).Type() != vm.NullType {
		max := int(arg(ctx, 4).ToInt())
		if max >= 0 && max < len(data) {
			data = data[:max]
		}
	}
	return vm.Str(string(data))
}

func filePutContentsFn(ctx *vm.Context) vm.Value {
	path := normalizePath(arg0(ctx).ToString())
	// data:// и php://input/stdin — только для чтения: запись отклоняется с
	// PHP-Notice, как в Zend.
	if strings.HasPrefix(path, "data:") || path == "php://input" || path == "php://stdin" {
		ctx.VM().Notice("file_put_contents(): Stream is not writable")
		return vm.Bool(false)
	}
	// Контекст (4-й аргумент), если задан, должен быть stream-context.
	if len(ctx.Args()) >= 4 && arg(ctx, 3).Type() != vm.NullType {
		c := arg(ctx, 3)
		r := vm.AsResource(c)
		if c.Type() != vm.ResourceType || r == nil || r.Kind != "stream-context" {
			ctx.Throw(vm.NewThrowable("TypeError",
				"file_put_contents(): supplied resource is not a valid Stream-Context resource"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
	}
	var data []byte
	writeOK := true
	d := arg(ctx, 1)
	if d.Type() == vm.ArrayType {
		var b strings.Builder
		for _, v := range d.AsArray().Values() {
			s, err := ctx.VM().ToStringValue(v)
			if err != nil {
				break
			}
			b.WriteString(s)
		}
		data = []byte(b.String())
	} else if d.Type() == vm.ResourceType {
		h := fsHandleOf(d)
		if h == nil {
			ctx.Throw(vm.NewThrowable("TypeError",
				"file_put_contents(): supplied resource is not a valid stream resource"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
		rest, _ := io.ReadAll(h)
		data = rest
	} else if d.Type() == vm.ObjectType {
		s, err := ctx.VM().ToStringValue(d)
		if err != nil {
			data = []byte{}
			writeOK = false
		} else {
			data = []byte(s)
		}
	} else {
		data = []byte(d.ToString())
	}
	flags := 0
	if len(ctx.Args()) >= 3 {
		flags = int(arg(ctx, 2).ToInt())
	}
	var f *os.File
	var err error
	if !checkOpenBasedir(ctx.VM(), "file_put_contents", path, arg0(ctx).ToString()) {
		ctx.VM().Warning("file_put_contents(" + arg0(ctx).ToString() + "): Failed to open stream: Operation not permitted")
		return vm.Bool(false)
	}
	umode := "w"
	if flags&8 != 0 {
		umode = "a"
	}
	if h, ok := openUserStream(ctx, path, umode); ok {
		n, werr := h.Write(data)
		h.Close()
		if werr != nil {
			return vm.Bool(false)
		}
		if n < len(data) {
			ctx.VM().Warning(fmt.Sprintf("file_put_contents(): Only %d of %d bytes written, possibly out of free disk space",
				n, len(data)))
		}
		if !writeOK {
			return vm.Bool(false)
		}
		return vm.Int(int64(n))
	}
	if flags&8 != 0 {
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
	} else {
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	}
	if err != nil {
		return vm.Bool(false)
	}
	defer f.Close()
	n, err := f.Write(data)
	if err != nil {
		return vm.Bool(false)
	}
	if !writeOK {
		return vm.Bool(false)
	}
	return vm.Int(int64(n))
}

func fileFn(ctx *vm.Context) vm.Value {
	path := pathString(ctx, 0)
	flags := 0
	if len(ctx.Args()) >= 2 {
		flags = int(arg(ctx, 1).ToInt())
	}
	if flags&^23 != 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "file(): Argument #2 ($flags) must be a valid flag value"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if flags&1 != 0 {
		path, _ = fsResolveIncludePath(path, ctx)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if !checkOpenBasedir(ctx.VM(), "file", path, arg0(ctx).ToString()) {
		ctx.VM().Warning("file(" + arg0(ctx).ToString() + "): Failed to open stream: Operation not permitted")
		return vm.Bool(false)
	}
	data, ok := readPathBytes(path)
	if !ok {
		ctx.VM().Warning("file(" + arg0(ctx).ToString() + "): Failed to open stream: " + fileOpenReason(path, "r"))
		return vm.Bool(false)
	}
	ignoreNL := flags&2 != 0
	skipEmpty := flags&4 != 0
	out := vm.NewArray()
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		line := string(data[start : i+1])
		start = i + 1
		if ignoreNL {
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			// FILE_SKIP_EMPTY_LINES учитывается только вместе с
			// FILE_IGNORE_NEW_LINES (особенность реализации PHP).
			if skipEmpty && strings.TrimRight(line, "\r") == "" {
				continue
			}
		}
		out.Append(vm.MakeString(line))
	}
	if start < len(data) {
		line := string(data[start:])
		if ignoreNL {
			line = strings.TrimSuffix(line, "\r")
		}
		out.Append(vm.MakeString(line))
	}
	return vm.MakeArray(out)
}

func readfileFn(ctx *vm.Context) vm.Value {
	raw := arg0(ctx).ToString()
	if raw == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "Path cannot be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	path := pathString(ctx, 0)
	if len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool() {
		path, _ = fsResolveIncludePath(path, ctx)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if !checkOpenBasedir(ctx.VM(), "readfile", path, raw) {
		ctx.VM().Warning("readfile(" + raw + "): Failed to open stream: Operation not permitted")
		return vm.Bool(false)
	}
	data, ok := readPathBytes(path)
	if !ok {
		ctx.VM().Warning("readfile(" + raw + "): Failed to open stream: " + fileOpenReason(path, "r"))
		return vm.Bool(false)
	}
	ctx.Print(string(data))
	return vm.Int(int64(len(data)))
}

// --- проверки пути ---

func fileExistsFn(ctx *vm.Context) vm.Value {
	raw := arg0(ctx).ToString()
	if !checkOpenBasedir(ctx.VM(), "file_exists", raw, raw) {
		return vm.Bool(false)
	}
	// Bug #53226: слишком длинный путь не stat'ится (PHP_MAXPATHLEN = 4096).
	if len(raw) >= 4096 {
		ctx.VM().Warning(fmt.Sprintf(
			"file_exists(): File name is longer than the maximum allowed path length on this platform (%d): %s",
			4096, raw))
		return vm.Bool(false)
	}
	p := pathString(ctx, 0)
	if p2, unknown := unknownWrapperFallback(ctx, "file_exists", raw); unknown {
		p = p2
	}
	_, err := os.Stat(p)
	return vm.Bool(err == nil)
}

// isFileKindChecked — is_file/is_dir/is_link с проверкой open_basedir.
func isFileKindChecked(ctx *vm.Context, kind string) vm.Value {
	raw := arg0(ctx).ToString()
	if !checkOpenBasedir(ctx.VM(), "is_"+kind, raw, raw) {
		return vm.Bool(false)
	}
	return vm.Bool(isFileKind(arg0(ctx), kind))
}

// accessChecked — is_readable/is_writable/is_executable с проверкой open_basedir.
func accessChecked(ctx *vm.Context, fn string, mode uint32) vm.Value {
	raw := arg0(ctx).ToString()
	if !checkOpenBasedir(ctx.VM(), fn, raw, raw) {
		return vm.Bool(false)
	}
	return vm.Bool(accessCheck(pathString(ctx, 0), mode))
}

func isFileKind(s vm.Slot, kind string) bool {
	fi, err := os.Lstat(normalizePath(s.ToString()))
	if err != nil {
		return false
	}
	switch kind {
	case "link":
		return fi.Mode()&os.ModeSymlink != 0
	case "dir":
		return fi.IsDir()
	case "file":
		return fi.Mode().IsRegular()
	}
	return false
}

func accessCheck(path string, mode uint32) bool {
	if path == "" {
		return false
	}
	return syscall.Access(path, mode) == nil
}

func filesizeFn(ctx *vm.Context) vm.Value {
	if !checkOpenBasedir(ctx.VM(), "filesize", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if s, handled := normalizedUserStat(ctx, arg0(ctx).ToString(), false); handled {
		if s.Type() == vm.ArrayType {
			if v, ok := s.AsArray().Get(vm.MakeStrKey("size")); ok {
				return vm.MakeInt(v.ToInt())
			}
		}
		statWarnFn(ctx, "filesize", arg0(ctx).ToString(), false)
		return vm.Bool(false)
	}
	fi, err := os.Stat(pathString(ctx, 0))
	if err != nil {
		statWarnFn(ctx, "filesize", arg0(ctx).ToString(), false)
		return vm.Bool(false)
	}
	return vm.Int(fi.Size())
}

// statWarnFn печатает PHP-предупреждение о неудачном stat/lstat. Для пустого
// пути предупреждение не выводится (как в Zend).
func statWarnFn(ctx *vm.Context, fn, raw string, lstat bool) {
	if raw == "" {
		return
	}
	if strings.IndexByte(raw, 0) >= 0 {
		ctx.VM().Warning(fn + "(): Filename contains null byte")
		return
	}
	if lstat {
		ctx.VM().Warning(fn + "(): Lstat failed for " + raw)
	} else {
		ctx.VM().Warning(fn + "(): stat failed for " + raw)
	}
}

// stdStreamConst возвращает ресурсы-константы STDIN/STDOUT/STDERR.
func stdStreamConst(name string, vmi *vm.VM) (vm.Slot, bool) {
	// STDIN/STDOUT/STDERR — константы процесса, создаются один раз с
	// фиксированными id 1..3 (как PHP CLI).
	if v, ok := stdStreamRes[name]; ok {
		return v, true
	}
	mk := func(path string, fd int, id int64) vm.Slot {
		var f *os.File
		if fd < 0 {
			f, _ = os.CreateTemp("", "php-stdin")
			if f != nil && len(phpStdinRaw) > 0 {
				f.Write(phpStdinRaw)
				f.Seek(0, io.SeekStart)
			}
		} else {
			f = dupFile(fd, path)
		}
		if f == nil {
			return vm.MakeNull()
		}
		h := &fsHandle{f: f, path: path, mode: "r", push: -1, canRead: true, canWrite: fd >= 0, tmp: fd < 0}
		if fd >= 0 && vmi != nil {
			h.outVM = vmi
		}
		s := vm.MakeResource(id, "stream", h)
		h.res = vm.AsResource(s)
		fsHandles[id] = h
		if id >= fsNextID {
			fsNextID = id + 1
		}
		if stdStreamRes == nil {
			stdStreamRes = map[string]vm.Slot{}
		}
		stdStreamRes[name] = s
		return s
	}
	switch name {
	case "STDIN":
		return mk("php://stdin", -1, 1), true
	case "STDOUT":
		return mk("php://stdout", 1, 2), true
	case "STDERR":
		return mk("php://stderr", 2, 3), true
	}
	return vm.MakeNull(), false
}

// stdStreamRes — кэш ресурсов-констант STDIN/STDOUT/STDERR.
var stdStreamRes map[string]vm.Slot

func sysStat(s vm.Slot) (*syscall.Stat_t, os.FileInfo, bool) {
	fi, err := os.Stat(s.ToString())
	if err != nil {
		return nil, nil, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fi, false
	}
	return st, fi, true
}

func sysLstat(s vm.Slot) (*syscall.Stat_t, os.FileInfo, bool) {
	fi, err := os.Lstat(s.ToString())
	if err != nil {
		return nil, nil, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fi, false
	}
	return st, fi, true
}

func timeFieldFn(ctx *vm.Context, field string) vm.Value {
	fn := "filemtime"
	if field == "atime" {
		fn = "fileatime"
	} else if field == "ctime" {
		fn = "filectime"
	}
	if !checkOpenBasedir(ctx.VM(), fn, arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if s, handled := normalizedUserStat(ctx, arg0(ctx).ToString(), false); handled {
		if s.Type() == vm.ArrayType {
			if v, ok := s.AsArray().Get(vm.MakeStrKey(field)); ok {
				return vm.MakeInt(v.ToInt())
			}
		}
		statWarnFn(ctx, fn, arg0(ctx).ToString(), false)
		return vm.Bool(false)
	}
	st, _, ok := sysStat(arg0(ctx))
	if !ok {
		statWarnFn(ctx, fn, arg0(ctx).ToString(), false)
		return vm.Bool(false)
	}
	var tv syscall.Timespec
	switch field {
	case "mtime":
		tv = st.Mtim
	case "atime":
		tv = st.Atim
	default:
		tv = st.Ctim
	}
	return vm.Int(tv.Sec)
}

func filepermsFn(ctx *vm.Context) vm.Value {
	if !checkOpenBasedir(ctx.VM(), "fileperms", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	st, _, ok := sysLstat(arg0(ctx))
	if !ok {
		statWarnFn(ctx, "fileperms", arg0(ctx).ToString(), false)
		return vm.Bool(false)
	}
	return vm.Int(int64(st.Mode))
}

func filetypeFn(ctx *vm.Context) vm.Value {
	if !checkOpenBasedir(ctx.VM(), "filetype", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	st, _, ok := sysLstat(arg0(ctx))
	if !ok {
		statWarnFn(ctx, "filetype", arg0(ctx).ToString(), true)
		return vm.Bool(false)
	}
	switch st.Mode & syscall.S_IFMT {
	case syscall.S_IFREG:
		return vm.Str("file")
	case syscall.S_IFDIR:
		return vm.Str("dir")
	case syscall.S_IFLNK:
		return vm.Str("link")
	case syscall.S_IFCHR:
		return vm.Str("char")
	case syscall.S_IFBLK:
		return vm.Str("block")
	case syscall.S_IFIFO:
		return vm.Str("fifo")
	case syscall.S_IFSOCK:
		return vm.Str("socket")
	}
	return vm.Str("unknown")
}

func ownerFieldFn(ctx *vm.Context, owner bool) vm.Value {
	fn := "fileowner"
	if !owner {
		fn = "filegroup"
	}
	if !checkOpenBasedir(ctx.VM(), fn, arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	st, _, ok := sysStat(arg0(ctx))
	if !ok {
		statWarnFn(ctx, fn, arg0(ctx).ToString(), false)
		return vm.Bool(false)
	}
	if owner {
		return vm.Int(int64(st.Uid))
	}
	return vm.Int(int64(st.Gid))
}

func fileinodeFn(ctx *vm.Context) vm.Value {
	if !checkOpenBasedir(ctx.VM(), "fileinode", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	st, _, ok := sysStat(arg0(ctx))
	if !ok {
		statWarnFn(ctx, "fileinode", arg0(ctx).ToString(), false)
		return vm.Bool(false)
	}
	return vm.Int(int64(st.Ino))
}

func statArray(st *syscall.Stat_t) *vm.Array {
	out := vm.NewArray()
	names := []string{"dev", "ino", "mode", "nlink", "uid", "gid", "rdev", "size", "atime", "mtime", "ctime", "blksize", "blocks"}
	vals := []int64{
		int64(st.Dev), int64(st.Ino), int64(st.Mode), int64(st.Nlink),
		int64(st.Uid), int64(st.Gid), int64(st.Rdev), st.Size,
		st.Atim.Sec, st.Mtim.Sec, st.Ctim.Sec, st.Blksize, st.Blocks,
	}
	// Сначала все числовые ключи 0..N-1, затем строковые (как в PHP).
	for i := range names {
		out.SetInt(int64(i), vm.MakeInt(vals[i]))
	}
	for i, name := range names {
		out.SetStr(name, vm.MakeInt(vals[i]))
	}
	return out
}

// statFieldOrder — канонический порядок полей struct stat (числовые индексы).
var statFieldOrder = []string{"dev", "ino", "mode", "nlink", "uid", "gid", "rdev",
	"size", "atime", "mtime", "ctime", "blksize", "blocks"}

// userStatCache — кэш результатов url_stat пользовательских обёрток (stat
// cache в Zend): stat()/filesize()/filemtime() не вызывают url_stat повторно.
var userStatCache = map[string]vm.Slot{}

// clearUserStatCache сбрасывает кэш stat для пользовательских обёрток.
func clearUserStatCache() { userStatCache = map[string]vm.Slot{} }

// normalizedUserStat вызывает url_stat пользовательской обёртки и приводит
// результат к PHP-виду: числовые индексы 0..12 в каноническом порядке плюс
// строковые ключи. Возвращает (значение, обработано).
func normalizedUserStat(ctx *vm.Context, raw string, lstat bool) (vm.Slot, bool) {
	key := raw
	if lstat {
		key = "l:" + raw
	}
	if v, ok := userStatCache[key]; ok {
		return v, true
	}
	opt := vm.MakeInt(0)
	if lstat {
		opt = vm.MakeInt(1)
	}
	res, handled := callUserWrapper(ctx, raw, "url_stat", vm.MakeString(raw), opt)
	if ctx.HasThrown() {
		return *new(vm.Slot), *new(bool)
	}
	if !handled {
		return vm.MakeNull(), false
	}
	if res.Type() != vm.ArrayType {
		userStatCache[key] = vm.MakeBool(false)
		return vm.MakeBool(false), true
	}
	out := vm.NewArray()
	for i, name := range statFieldOrder {
		val := vm.MakeInt(-1)
		if v, ok := res.AsArray().Get(vm.MakeStrKey(name)); ok {
			val = v
		}
		_ = out.SetInt(int64(i), val)
	}
	for _, name := range statFieldOrder {
		if v, ok := res.AsArray().Get(vm.MakeStrKey(name)); ok {
			_ = out.SetStr(name, v)
		}
	}
	s := vm.MakeArray(out)
	userStatCache[key] = s
	return s, true
}

// userStatField достаёт поле из нормализованного результата url_stat.
func userStatField(ctx *vm.Context, raw, field string) (int64, bool) {
	s, handled := normalizedUserStat(ctx, raw, false)
	if ctx.HasThrown() {
		return *new(int64), *new(bool)
	}
	if !handled || s.Type() != vm.ArrayType {
		return 0, false
	}
	v, ok := s.AsArray().Get(vm.MakeStrKey(field))
	if !ok {
		return 0, false
	}
	return v.ToInt(), true
}

func statFn(ctx *vm.Context, lstat bool) vm.Value {
	fn := "stat"
	if lstat {
		fn = "lstat"
	}
	if !checkOpenBasedir(ctx.VM(), fn, arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if s, handled := normalizedUserStat(ctx, arg0(ctx).ToString(), lstat); handled {
		return s
	}
	var st *syscall.Stat_t
	var ok bool
	if lstat {
		st, _, ok = sysLstat(arg0(ctx))
	} else {
		st, _, ok = sysStat(arg0(ctx))
	}
	if !ok {
		fn := "stat"
		if lstat {
			fn = "lstat"
		}
		statWarnFn(ctx, fn, arg0(ctx).ToString(), lstat)
		return vm.Bool(false)
	}
	return vm.MakeArray(statArray(st))
}

func fstatFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fstat")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.us != nil {
		if o := h.us.vm.SlotObject(h.us.obj); o != nil {
			if ci := h.us.vm.FindClass(o.ClassName()); ci != nil && hasMethod(ci, "stream_stat") {
				res, ok := h.us.callMethod("stream_stat")
				if !ok || res.Type() != vm.ArrayType {
					return vm.Bool(false)
				}
				return res
			}
		}
	}
	fi, err := h.Stat()
	if err != nil {
		return vm.Bool(false)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return vm.Bool(false)
	}
	return vm.MakeArray(statArray(st))
}

func readlinkFn(ctx *vm.Context) vm.Value {
	if !checkOpenBasedir(ctx.VM(), "readlink", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	t, err := os.Readlink(pathString(ctx, 0))
	if err != nil {
		ctx.VM().Warning("readlink(): " + linkErrReason(err))
		return vm.Bool(false)
	}
	return vm.Str(t)
}

// --- операции ---

func unlinkFn(ctx *vm.Context) vm.Value {
	raw := arg0(ctx).ToString()
	if res, handled := callUserWrapper(ctx, raw, "unlink", arg0(ctx)); handled {
		if res.Type() == vm.BoolType {
			return res
		}
		return vm.Bool(true)
	}
	if !checkOpenBasedir(ctx.VM(), "unlink", raw, raw) {
		return vm.Bool(false)
	}
	if err := os.Remove(pathString(ctx, 0)); err != nil {
		if raw == "" {
			ctx.VM().Warning("unlink(): " + linkErrReason(err))
		} else {
			ctx.VM().Warning("unlink(" + raw + "): " + linkErrReason(err))
		}
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

func renameFn(ctx *vm.Context) vm.Value {
	old := arg0(ctx).ToString()
	newRaw := arg(ctx, 1).ToString()
	if sch, ok := userScheme(old); ok {
		if _, reg := streamWrappers[strings.ToLower(sch)]; reg {
			nsch, nok := userScheme(newRaw)
			if !nok || strings.ToLower(nsch) != strings.ToLower(sch) {
				ctx.VM().Warning("rename(): Cannot rename a file across wrapper types")
				return vm.Bool(false)
			}
			clearUserStatCache()
		}
	}
	if res, handled := callUserWrapper(ctx, old, "rename", arg0(ctx), arg(ctx, 1)); handled {
		if res.Type() == vm.BoolType {
			return res
		}
		return vm.Bool(true)
	}
	if !checkOpenBasedir(ctx.VM(), "rename", old, old) {
		return vm.Bool(false)
	}
	if !checkOpenBasedir(ctx.VM(), "rename", newRaw, newRaw) {
		return vm.Bool(false)
	}
	if err := os.Rename(pathString(ctx, 0), pathString(ctx, 1)); err != nil {
		ctx.VM().Warning("rename(" + old + "," + arg(ctx, 1).ToString() + "): " + linkErrReason(err))
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

func copyFn(ctx *vm.Context) vm.Value {
	sp := pathString(ctx, 0)
	dp := pathString(ctx, 1)
	if !checkOpenBasedir(ctx.VM(), "copy", sp, arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if !checkOpenBasedir(ctx.VM(), "copy", dp, arg(ctx, 1).ToString()) {
		ctx.VM().Warning("copy(" + arg(ctx, 1).ToString() + "): Failed to open stream: Operation not permitted")
		return vm.Bool(false)
	}
	if rp, err := filepath.EvalSymlinks(sp); err == nil {
		if rq, err2 := filepath.EvalSymlinks(dp); err2 == nil && rp == rq {
			return vm.Bool(false)
		}
	}
	if abs, err := filepath.Abs(sp); err == nil {
		if abd, err2 := filepath.Abs(dp); err2 == nil && abs == abd {
			return vm.Bool(false)
		}
	}
	if fi, err := os.Stat(sp); err == nil && fi.IsDir() {
		ctx.VM().Warning("copy(): The first argument to copy() function cannot be a directory")
		return vm.Bool(false)
	}
	// Источники с обёрткой (data://, пользовательские) читаем через потоковый слой.
	if strings.Contains(sp, "://") && !strings.HasPrefix(sp, "file://") {
		if h, ok := openUserStream(ctx, sp, "r"); ok {
			data, _ := io.ReadAll(h)
			h.Close()
			if err := os.WriteFile(dp, data, 0666); err != nil {
				return vm.Bool(false)
			}
			return vm.Bool(true)
		}
		if data, ok := readPathBytes(sp); ok {
			if err := os.WriteFile(dp, data, 0666); err != nil {
				return vm.Bool(false)
			}
			return vm.Bool(true)
		}
	}
	// Назначение с пользовательской обёрткой: потоковая запись порциями.
	if scheme, ok := userScheme(dp); ok {
		if _, reg := streamWrappers[strings.ToLower(scheme)]; reg {
			src, serr := os.Open(sp)
			if serr != nil {
				ctx.VM().Warning("copy(" + sp + "): Failed to open stream: " + linkErrReason(serr))
				return vm.Bool(false)
			}
			defer src.Close()
			h, ok2 := openUserStream(ctx, dp, "w")
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			if !ok2 {
				ctx.VM().Warning("copy(" + dp + "): Failed to open stream: " + openFailReason(dp, "w"))
				return vm.Bool(false)
			}
			buf := make([]byte, h.chunk())
			for {
				n, rerr := src.Read(buf)
				if n > 0 {
					if _, werr := h.Write(buf[:n]); werr != nil {
						h.Close()
						return vm.Bool(false)
					}
				}
				if rerr == io.EOF {
					break
				}
				if rerr != nil {
					h.Close()
					return vm.Bool(false)
				}
			}
			h.Close()
			return vm.Bool(true)
		}
	}
	src, err := os.Open(sp)
	if err != nil {
		ctx.VM().Warning("copy(" + sp + "): Failed to open stream: " + linkErrReason(err))
		return vm.Bool(false)
	}
	defer src.Close()
	dst, err := os.OpenFile(dp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		ctx.VM().Warning("copy(" + sp + "): Failed to open stream: " + linkErrReason(err))
		return vm.Bool(false)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

func mkdirFn(ctx *vm.Context) vm.Value {
	if res, handled := callUserWrapper(ctx, arg0(ctx).ToString(), "mkdir", arg0(ctx),
		vm.MakeInt(arg(ctx, 1).ToInt()), vm.MakeInt(arg(ctx, 2).ToInt())); handled {
		if res.Type() == vm.BoolType {
			return res
		}
		return vm.Bool(true)
	}
	path := pathString(ctx, 0)
	if !checkOpenBasedir(ctx.VM(), "mkdir", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	perm := os.FileMode(0777)
	if len(ctx.Args()) >= 2 {
		perm = os.FileMode(arg(ctx, 1).ToInt() & 0777)
	}
	recursive := len(ctx.Args()) >= 3 && arg(ctx, 2).ToBool()
	var err error
	if recursive {
		err = os.MkdirAll(path, perm)
	} else {
		err = os.Mkdir(path, perm)
	}
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

func rmdirFn(ctx *vm.Context) vm.Value {
	if res, handled := callUserWrapper(ctx, arg0(ctx).ToString(), "rmdir", arg0(ctx), vm.MakeInt(arg(ctx, 1).ToInt())); handled {
		if res.Type() == vm.BoolType {
			return res
		}
		return vm.Bool(true)
	}
	if !checkOpenBasedir(ctx.VM(), "rmdir", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if err := os.Remove(pathString(ctx, 0)); err != nil {
		ctx.VM().Warning("rmdir(" + arg0(ctx).ToString() + "): " + linkErrReason(err))
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// arrayOf создаёт vm-массив с перечисленными значениями (числовые ключи).
func arrayOf(vals ...vm.Slot) *vm.Array {
	a := vm.NewArray()
	for _, v := range vals {
		_, _ = a.Append(v)
	}
	return a
}

// userMetaCall вызывает stream_metadata пользовательской обёртки (если URL
// принадлежит ей). Возвращает (значение, обработано).
func userMetaCall(ctx *vm.Context, path string, option int64, v vm.Slot) (vm.Value, bool) {
	res, handled := callUserWrapper(ctx, path, "stream_metadata", vm.MakeString(path), vm.MakeInt(option), v)
	if ctx.HasThrown() {
		return *new(vm.Value), *new(bool)
	}
	if !handled {
		return vm.MakeNull(), false
	}
	if res.Type() == vm.BoolType {
		return res, true
	}
	return vm.Bool(true), true
}

func chmodFn(ctx *vm.Context) vm.Value {
	if r, ok := userMetaCall(ctx, arg0(ctx).ToString(), 6, vm.MakeInt(arg(ctx, 1).ToInt())); ok {
		return r
	}
	if !checkOpenBasedir(ctx.VM(), "chmod", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if err := os.Chmod(pathString(ctx, 0), os.FileMode(arg(ctx, 1).ToInt()&07777)); err != nil {
		ctx.VM().Warning("chmod(): " + linkErrReason(err))
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

func chownFn(ctx *vm.Context) vm.Value {
	v := arg(ctx, 1)
	opt := int64(3)
	mv := vm.MakeInt(v.ToInt())
	if v.Type() == vm.StringType {
		opt = 2
		mv = vm.MakeString(v.ToString())
	}
	if r, ok := userMetaCall(ctx, arg0(ctx).ToString(), opt, mv); ok {
		return r
	}
	return vm.Bool(true)
}

func chgrpFn(ctx *vm.Context) vm.Value {
	v := arg(ctx, 1)
	opt := int64(5)
	mv := vm.MakeInt(v.ToInt())
	if v.Type() == vm.StringType {
		opt = 4
		mv = vm.MakeString(v.ToString())
	}
	if r, ok := userMetaCall(ctx, arg0(ctx).ToString(), opt, mv); ok {
		return r
	}
	return vm.Bool(true)
}

func touchFn(ctx *vm.Context) vm.Value {
	var meta vm.Slot
	switch {
	case len(ctx.Args()) >= 3:
		meta = vm.MakeArray(arrayOf(vm.MakeInt(arg(ctx, 1).ToInt()), vm.MakeInt(arg(ctx, 2).ToInt())))
	case len(ctx.Args()) >= 2:
		t := vm.MakeInt(arg(ctx, 1).ToInt())
		meta = vm.MakeArray(arrayOf(t, t))
	default:
		meta = vm.MakeArray(vm.NewArray())
	}
	if res, handled := callUserWrapper(ctx, arg0(ctx).ToString(), "stream_metadata",
		vm.MakeString(arg0(ctx).ToString()), vm.MakeInt(1), meta); handled {
		if res.Type() == vm.BoolType {
			return res
		}
		return vm.Bool(true)
	}
	if !checkOpenBasedir(ctx.VM(), "touch", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	path := pathString(ctx, 0)
	if _, err := os.Stat(path); err != nil {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			ctx.VM().Warning("touch(): Unable to create file " + arg0(ctx).ToString() +
				" because " + linkErrReason(err))
			return vm.Bool(false)
		}
		f.Close()
	}
	now := time.Now()
	mt := now
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() != vm.NullType {
		mt = time.Unix(arg(ctx, 1).ToInt(), 0)
	}
	at := mt
	if len(ctx.Args()) >= 3 && arg(ctx, 2).Type() != vm.NullType {
		at = time.Unix(arg(ctx, 2).ToInt(), 0)
	}
	if err := os.Chtimes(path, at, mt); err != nil {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

func realpathFn(ctx *vm.Context) vm.Value {
	if arg0(ctx).ToString() == "" {
		if wd, err := os.Getwd(); err == nil {
			return vm.Str(wd)
		}
	}
	p := pathString(ctx, 0)
	if !checkOpenBasedir(ctx.VM(), "realpath", p, "") {
		return vm.Bool(false)
	}
	if _, err := os.Lstat(p); err != nil {
		return vm.Bool(false)
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return vm.Bool(false)
	}
	abs, err := filepath.Abs(r)
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Str(abs)
}

func tempnamFn(ctx *vm.Context) vm.Value {
	dir := ""
	if len(ctx.Args()) >= 1 {
		dir = arg(ctx, 0).ToString()
	}
	prefix := "php"
	if len(ctx.Args()) >= 2 {
		p := arg(ctx, 1).ToString()
		if p != "" {
			if len(p) > 63 {
				p = p[:63]
			}
			prefix = p
		}
	}
	rawDir := dir
	if rawDir != "" {
		if !checkOpenBasedir(ctx.VM(), "tempnam", rawDir, rawDir) {
			return vm.Bool(false)
		}
	}
	notice := false
	if dir == "" || !accessCheck(dir, 2) {
		if dir != "" {
			notice = true
		}
		dir = os.TempDir()
	}
	if notice {
		ctx.VM().Notice("tempnam(): file created in the system's temporary directory")
	}
	if !checkOpenBasedir(ctx.VM(), "tempnam", dir, dir) {
		return vm.Bool(false)
	}
	f, err := os.CreateTemp(dir, prefix)
	if err != nil {
		return vm.Bool(false)
	}
	name := f.Name()
	f.Close()
	os.Chmod(name, 0600)
	return vm.Str(name)
}

func scandirFn(ctx *vm.Context) vm.Value {
	dir := pathString(ctx, 0)
	if a := arg0(ctx); a.Type() == vm.StringType && a.StringVal() == "" {
		// PHP 8: пустое имя каталога — ValueError (bug41693).
		ctx.Throw(vm.NewThrowable("ValueError", "scandir(): Argument #1 ($directory) cannot be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if !checkOpenBasedir(ctx.VM(), "scandir", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		raw := arg0(ctx).ToString()
		ctx.VM().Warning("scandir(" + raw + "): Failed to open directory: Operation not permitted")
		ctx.VM().Warning("scandir(): (errno 1): Operation not permitted")
		return vm.Bool(false)
	}
	// glob:// wrapper: возвращает только совпадения (без "." и "..").
	if len(dir) >= 7 && strings.EqualFold(dir[:7], "glob://") {
		ms, _ := filepath.Glob(dir[7:])
		sort.Strings(ms)
		out := vm.NewArray()
		for _, n := range ms {
			out.Append(vm.MakeString(n))
		}
		return vm.MakeArray(out)
	}
	order := 0
	if len(ctx.Args()) >= 2 {
		// Zend приводит аргумент к C int: PHP_INT_MAX -> -1.
		order = int(int32(arg(ctx, 1).ToInt()))
	}
	// Пользовательская директория (dir_opendir/dir_readdir/dir_closedir).
	if scheme, ok := userScheme(dir); ok {
		if _, reg := streamWrappers[strings.ToLower(scheme)]; reg {
			u, ok2 := openUserDir(ctx, dir)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			if !ok2 {
				return vm.Bool(false)
			}
			var list []string
			for {
				res, rok := u.callMethod("dir_readdir")
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				if !rok {
					break
				}
				if res.Type() == vm.BoolType && !res.ToBool() {
					break
				}
				s := res.ToString()
				if s == "" {
					break
				}
				list = append(list, s)
			}
			u.callMethod("dir_closedir")
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			if order == 0 {
				sort.Strings(list)
			} else if order == 1 {
				sort.Sort(sort.Reverse(sort.StringSlice(list)))
			}
			out := vm.NewArray()
			for _, n := range list {
				out.Append(vm.MakeString(n))
			}
			return vm.MakeArray(out)
		}
	}
	var list []string
	if order == 2 {
		// SCANDIR_SORT_NONE: порядок чтения каталога как есть.
		d, err := os.Open(dir)
		if err != nil {
			return vm.Bool(false)
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return vm.Bool(false)
		}
		list = append(list, ".", "..")
		list = append(list, names...)
	} else {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return vm.Bool(false)
		}
		list = append(list, ".", "..")
		for _, e := range entries {
			list = append(list, e.Name())
		}
	}
	// Zend: сортировка только при order == 0; всё ненулевое кроме
	// SCANDIR_SORT_NONE (2) — по убыванию.
	if order == 0 {
		sort.Strings(list)
	} else if order != 2 {
		sort.Sort(sort.Reverse(sort.StringSlice(list)))
	}
	out := vm.NewArray()
	for _, n := range list {
		out.Append(vm.MakeString(n))
	}
	return vm.MakeArray(out)
}

func globFn(ctx *vm.Context) vm.Value {
	pattern := pathString(ctx, 0)
	flags := 0
	if len(ctx.Args()) >= 2 {
		flags = int(arg(ctx, 1).ToInt())
	}
	patterns := []string{pattern}
	if flags&128 != 0 {
		patterns = expandBraces(pattern)
	}
	// open_basedir: шаблон без метасимволов, выходящий за разрешённые пути,
	// даёт false (без предупреждения, как PHP). Для шаблонов с метасимволами
	// результаты молча фильтруются.
	hasMeta := strings.ContainsAny(pattern, "*?[")
	if !hasMeta && !openBasedirAllows(pattern) {
		return vm.Bool(false)
	}
	var matches []string
	for _, p := range patterns {
		ms, _ := filepath.Glob(p)
		if strings.HasPrefix(p, "./") {
			for i := range ms {
				if !strings.HasPrefix(ms[i], "./") {
					ms[i] = "./" + ms[i]
				}
			}
		}
		for _, m := range ms {
			if strings.HasPrefix(filepath.Base(m), ".") && !strings.HasPrefix(filepath.Base(p), ".") {
				continue
			}
			if !openBasedirAllows(m) {
				continue
			}
			if flags&8192 != 0 {
				if fi, err := os.Stat(m); err != nil || !fi.IsDir() {
					continue
				}
			}
			if flags&2 != 0 {
				if fi, err := os.Stat(m); err == nil && fi.IsDir() {
					m += "/"
				}
			}
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 {
		if flags&16 != 0 {
			out := vm.NewArray()
			out.Append(vm.MakeString(pattern))
			return vm.MakeArray(out)
		}
		return vm.MakeArray(vm.NewArray())
	}
	if flags&4 == 0 {
		sort.Strings(matches)
	}
	out := vm.NewArray()
	for _, m := range matches {
		out.Append(vm.MakeString(m))
	}
	return vm.MakeArray(out)
}

func expandBraces(pattern string) []string {
	start := strings.IndexByte(pattern, '{')
	if start < 0 {
		return []string{pattern}
	}
	depth := 0
	end := -1
	for i := start; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return []string{pattern}
	}
	prefix := pattern[:start]
	suffix := pattern[end+1:]
	var out []string
	for _, alt := range splitBrace(pattern[start+1 : end]) {
		for _, sub := range expandBraces(prefix + alt + suffix) {
			out = append(out, sub)
		}
	}
	return out
}

func splitBrace(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func popenFn(ctx *vm.Context) vm.Value {
	mode := arg(ctx, 1).ToString()
	switch mode {
	case "r", "rb", "w", "wb":
	default:
		ctx.Throw(vm.NewThrowable("ValueError",
			`popen(): Argument #2 ($mode) must be one of "r", "rb", "w", or "wb"`))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	return vm.Bool(false)
}

func symlinkFn(ctx *vm.Context) vm.Value {
	if !checkOpenBasedir(ctx.VM(), "symlink", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if !checkOpenBasedir(ctx.VM(), "symlink", arg(ctx, 1).ToString(), arg(ctx, 1).ToString()) {
		return vm.Bool(false)
	}
	if err := os.Symlink(pathString(ctx, 0), pathString(ctx, 1)); err != nil {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

func linkFn(ctx *vm.Context) vm.Value {
	if strings.IndexByte(arg0(ctx).ToString(), 0) >= 0 {
		ctx.Throw(vm.NewThrowable("ValueError",
			"link(): Argument #1 ($target) must not contain any null bytes"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if !checkOpenBasedir(ctx.VM(), "link", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if !checkOpenBasedir(ctx.VM(), "link", arg(ctx, 1).ToString(), arg(ctx, 1).ToString()) {
		return vm.Bool(false)
	}
	if err := os.Link(pathString(ctx, 0), pathString(ctx, 1)); err != nil {
		ctx.VM().Warning("link(): " + linkErrReason(err))
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

func linkErrReason(err error) string {
	if os.IsNotExist(err) {
		return "No such file or directory"
	}
	if os.IsExist(err) {
		return "File exists"
	}
	if os.IsPermission(err) {
		return "Permission denied"
	}
	return err.Error()
}

// linkinfoFn — linkinfo(): информация о жёсткой ссылке (-1, если нет).
func linkinfoFn(ctx *vm.Context) vm.Value {
	p := arg0(ctx).ToString()
	fi, err := os.Lstat(p)
	if err != nil {
		ctx.VM().Warning("linkinfo(): " + linkErrReason(err))
		return vm.Int(-1)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			return vm.Int(int64(st.Dev))
		}
		return vm.Int(1)
	}
	return vm.Int(0)
}

func diskSpaceFn(ctx *vm.Context, name string, free bool) vm.Value {
	if strings.IndexByte(arg0(ctx).ToString(), 0) >= 0 {
		ctx.Throw(vm.NewThrowable("ValueError",
			name+"(): Argument #1 ($directory) must not contain any null bytes"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	path := pathString(ctx, 0)
	if !checkOpenBasedir(ctx.VM(), name, path, path) {
		return vm.Bool(false)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return vm.Bool(false)
	}
	bs := int64(st.Bsize)
	if free {
		return vm.Float(float64(st.Bavail * uint64(bs)))
	}
	return vm.Float(float64(st.Blocks * uint64(bs)))
}

func chdirFn(ctx *vm.Context) vm.Value {
	if !checkOpenBasedir(ctx.VM(), "chdir", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		return vm.Bool(false)
	}
	if err := os.Chdir(pathString(ctx, 0)); err != nil {
		ctx.VM().Warning("chdir(): " + errnoReason(err))
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// errnoReason форматирует ошибку как PHP: "<Strerror> (errno <N>)"
// (напр. "Not a directory (errno 20)"). Текст — из syscall.Errno.
func errnoReason(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		s := errno.Error()
		if s != "" {
			s = strings.ToUpper(s[:1]) + s[1:]
		}
		return s + " (errno " + strconv.Itoa(int(errno)) + ")"
	}
	return err.Error()
}

// --- каталоги ---

func opendirFn(ctx *vm.Context) vm.Value {
	ensureDefaultStreamContext()
	if res, handled := callUserWrapper(ctx, arg0(ctx).ToString(), "dir_opendir", arg0(ctx), vm.MakeInt(0)); handled {
		if !res.ToBool() {
			return vm.Bool(false)
		}
		h := &fsHandle{path: arg0(ctx).ToString(), mode: "r", dir: true, push: -1, canRead: true}
		fsLastDir = h
		return fsNewResource(h, "stream")
	}
	path := pathString(ctx, 0)
	if !checkOpenBasedir(ctx.VM(), "opendir", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		ctx.VM().Warning("opendir(" + arg0(ctx).ToString() + "): Failed to open directory: Operation not permitted")
		return vm.Bool(false)
	}
	d, err := os.Open(path)
	if err != nil {
		return vm.Bool(false)
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return vm.Bool(false)
	}
	h := &fsHandle{path: path, mode: "r", dir: true, names: append([]string{".", ".."}, names...), push: -1, canRead: true}
	fsLastDir = h
	// В PHP дескриптор каталога — потоковая обёртка "plainfile"/STDIO, поэтому
	// get_resource_type() и var_dump показывают тип (stream).
	return fsNewResource(h, "stream")
}

// dirFn — dir(): возвращает объект с открытым каталогом (свойство handle).
func dirFn(ctx *vm.Context) vm.Value {
	ensureDefaultStreamContext()
	path := pathString(ctx, 0)
	if !checkOpenBasedir(ctx.VM(), "dir", arg0(ctx).ToString(), arg0(ctx).ToString()) {
		ctx.VM().Warning("dir(" + arg0(ctx).ToString() + "): Failed to open directory: Operation not permitted")
		return vm.Bool(false)
	}
	d, err := os.Open(path)
	if err != nil {
		ctx.VM().Warning("dir(" + arg0(ctx).ToString() + "): Failed to open directory: " + linkErrReason(err))
		return vm.Bool(false)
	}
	names, _ := d.Readdirnames(-1)
	d.Close()
	h := &fsHandle{path: path, mode: "r", dir: true, names: append([]string{".", ".."}, names...), push: -1, canRead: true}
	fsLastDir = h
	res := fsNewResource(h, "stream")
	vmi := ctx.VMInstance()
	ci := vmi.FindClass("Directory")
	if ci == nil {
		return res
	}
	slot, o := vmi.NewObjectOf(ci)
	ci.SetProp(o, "path", vm.MakeString(path))
	ci.SetProp(o, "handle", res)
	return slot
}

// registerDirectoryMethods дополняет нативный класс Directory методами
// read/rewind/close (PHP 8: Directory::read() — та же операция, что readdir()
// по свойству handle; на закрытом ресурсе — TypeError).
func registerDirectoryMethods(classes []vm.ClassInfo) {
	for _, ci := range classes {
		if ci.Name() != "Directory" {
			continue
		}
		ci.Method("read", vm.Register("read", dirMethodRead))
		ci.Method("rewind", vm.Register("rewind", dirMethodRewind))
		ci.Method("close", vm.Register("close", dirMethodClose))
		return
	}
}

// dirThisHandle достаёт дескриптор каталога из свойства handle объекта
// Directory; на закрытом/невалидном ресурсе — TypeError как в PHP.
func dirThisHandle(ctx *vm.Context, method string) *fsHandle {
	msg := method + "(): supplied resource is not a valid Directory resource"
	o := ctx.VM().SlotObject(ctx.ThisSlot())
	if o == nil {
		ctx.Throw(vm.NewThrowable("TypeError", msg))
		return nil
	}
	hv, ok := o.Class().GetProp(o, "handle")
	if !ok {
		ctx.Throw(vm.NewThrowable("TypeError", msg))
		return nil
	}
	var hs vm.Slot
	vm.SlotOf(&hs, hv)
	h := fsHandleOf(hs)
	if h == nil {
		ctx.Throw(vm.NewThrowable("TypeError", msg))
		return nil
	}
	return h
}

func dirMethodRead(ctx *vm.Context) vm.Value {
	h := dirThisHandle(ctx, "Directory::read")
	if ctx.HasThrown() || h == nil {
		return *new(vm.Value)
	}
	if h.dirPos >= len(h.names) {
		return vm.Bool(false)
	}
	n := h.names[h.dirPos]
	h.dirPos++
	return vm.Str(n)
}

func dirMethodRewind(ctx *vm.Context) vm.Value {
	h := dirThisHandle(ctx, "Directory::rewind")
	if ctx.HasThrown() || h == nil {
		return *new(vm.Value)
	}
	h.dirPos = 0
	return vm.MakeNull()
}

func dirMethodClose(ctx *vm.Context) vm.Value {
	h := dirThisHandle(ctx, "Directory::close")
	if ctx.HasThrown() || h == nil {
		return *new(vm.Value)
	}
	fsResourceClosed(h)
	return vm.MakeNull()
}

// dirHandleArg извлекает дескриптор каталога из аргумента #0 с PHP-диагностикой
// неверного типа. fn — имя функции для сообщения.
func dirHandleArg(ctx *vm.Context, fn string) *fsHandle {
	s := arg0(ctx)
	if s.Type() != vm.ResourceType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError(fn, 1, "dir_handle", "resource", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(*fsHandle)
		}
	}
	r := vm.AsResource(s)
	if r == nil || r.Closed {
		ctx.Throw(vm.NewThrowable("TypeError",
			fn+"(): supplied resource is not a valid Directory resource"))
		if ctx.HasThrown() {
			return *new(*fsHandle)
		}
	}
	h, _ := r.Val.(*fsHandle)
	if h == nil || !h.dir {
		ctx.Throw(vm.NewThrowable("TypeError",
			fn+"(): Argument #1 ($dir_handle) must be a valid Directory resource"))
		if ctx.HasThrown() {
			return *new(*fsHandle)
		}
	}
	return h
}

// dirArgDefault — как dirHandleArg, но без аргумента берёт последний открытый
// каталог (PHP-семантика "default directory stream").
func dirArgDefault(ctx *vm.Context, fn string) *fsHandle {
	if len(ctx.Args()) == 0 {
		if fsLastDir == nil {
			ctx.Throw(vm.NewThrowable("TypeError", "No resource supplied"))
			if ctx.HasThrown() {
				return *new(*fsHandle)
			}
		}
		return fsLastDir
	}
	return dirHandleArg(ctx, fn)
}

func readdirFn(ctx *vm.Context) vm.Value {
	h := dirArgDefault(ctx, "readdir")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.dirPos >= len(h.names) {
		return vm.Bool(false)
	}
	n := h.names[h.dirPos]
	h.dirPos++
	return vm.Str(n)
}

func rewinddirFn(ctx *vm.Context) vm.Value {
	h := dirArgDefault(ctx, "rewinddir")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	h.dirPos = 0
	return vm.MakeNull()
}

func closedirFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		if fsLastDir == nil {
			ctx.Throw(vm.NewThrowable("TypeError", "No resource supplied"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		fsResourceClosed(fsLastDir)
		return vm.MakeNull()
	}
	h := dirHandleArg(ctx, "closedir")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	fsResourceClosed(h)
	return vm.MakeNull()
}

// --- fnmatch ---

func fnmatchFn(ctx *vm.Context) vm.Value {
	pattern := arg(ctx, 0).ToString()
	name := arg(ctx, 1).ToString()
	flags := 0
	if len(ctx.Args()) >= 3 {
		flags = int(arg(ctx, 2).ToInt())
	}
	pathname := flags&1 != 0
	noescape := flags&2 != 0
	period := flags&4 != 0
	casefold := flags&16 != 0
	return vm.Bool(fnmatchMatch(pattern, name, pathname, noescape, period, casefold))
}

func fnmatchMatch(pattern, name string, pathname, noescape, period, casefold bool) bool {
	if casefold {
		pattern = strings.ToLower(pattern)
		name = strings.ToLower(name)
	}
	if period && len(name) > 0 && name[0] == '.' {
		if len(pattern) == 0 || (pattern[0] != '.' && !(pattern[0] == '\\' && !noescape)) {
			return false
		}
	}
	pi, ni := 0, 0
	starP, starN := -1, 0
	for ni < len(name) {
		if pi < len(pattern) {
			c := pattern[pi]
			switch {
			case c == '*':
				starP = pi
				starN = ni
				pi++
				continue
			case c == '?':
				if pathname && name[ni] == '/' {
					// fail
				} else {
					pi++
					ni++
					continue
				}
			case c == '[':
				if end, ok := matchClass(pattern, pi, name[ni], pathname); ok {
					pi = end
					ni++
					continue
				}
			case c == '\\' && !noescape && pi+1 < len(pattern):
				if pattern[pi+1] == name[ni] {
					pi += 2
					ni++
					continue
				}
			default:
				if c == name[ni] {
					pi++
					ni++
					continue
				}
			}
		}
		if starP >= 0 {
			starN++
			ni = starN
			pi = starP + 1
			continue
		}
		return false
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// matchClass: pattern[i] == '[', возвращает (индекс после ']', совпало ли).
func matchClass(pattern string, i int, ch byte, pathname bool) (int, bool) {
	j := i + 1
	neg := false
	if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
		neg = true
		j++
	}
	matched := false
	first := true
	for j < len(pattern) && (pattern[j] != ']' || first) {
		first = false
		if j+2 < len(pattern) && pattern[j+1] == '-' && pattern[j+2] != ']' {
			lo, hi := pattern[j], pattern[j+2]
			if lo > hi {
				lo, hi = hi, lo
			}
			if ch >= lo && ch <= hi {
				matched = true
			}
			j += 3
		} else {
			if pattern[j] == ch {
				matched = true
			}
			j++
		}
	}
	if j >= len(pattern) {
		return i + 1, false
	}
	j++ // skip ']'
	if neg {
		matched = !matched
	}
	return j, matched
}

func fsyncNamed(ctx *vm.Context, name string) vm.Value {
	h := streamArg(ctx, 0, name)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	// Псевдо-потоки STDIN/STDOUT/STDERR/php://input — валидный поток, но
	// синхронизировать нечего: false без предупреждения.
	switch {
	case h.path == "php://stdin", h.path == "php://stdout", h.path == "php://stderr", h.path == "php://input":
		return vm.Bool(false)
	}
	if h.f == nil || h.isMemory {
		ctx.VM().Warning(name + "(): Can't fsync this stream!")
		return vm.Bool(false)
	}
	if err := syscall.Fsync(int(h.f.Fd())); err != nil {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// --- fgetcsv / fputcsv ---

func fgetcsvFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fgetcsv")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() != vm.NullType {
		if l := arg(ctx, 1).ToInt(); l < 0 || l > math.MaxInt64-1 {
			ctx.Throw(vm.NewThrowable("ValueError",
				"fgetcsv(): Argument #2 ($length) must be between 0 and 9223372036854775806"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
	}
	if len(ctx.Args()) >= 3 && len(arg(ctx, 2).ToString()) != 1 {
		ctx.Throw(vm.NewThrowable("ValueError", "fgetcsv(): Argument #3 ($separator) must be a single character"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if len(ctx.Args()) >= 4 && len(arg(ctx, 3).ToString()) != 1 {
		ctx.Throw(vm.NewThrowable("ValueError", "fgetcsv(): Argument #4 ($enclosure) must be a single character"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	sep := byte(',')
	if len(ctx.Args()) >= 3 && arg(ctx, 2).ToString() != "" {
		sep = arg(ctx, 2).ToString()[0]
	}
	enc := byte('"')
	if len(ctx.Args()) >= 4 && arg(ctx, 3).ToString() != "" {
		enc = arg(ctx, 3).ToString()[0]
	}
	esc := byte('\\')
	if len(ctx.Args()) >= 5 {
		if s := arg(ctx, 4).ToString(); s != "" {
			esc = s[0]
		} else {
			esc = 0
		}
	}
	if esc == enc {
		esc = 0
	}
	// Максимальная длина строки (bytes) или -1 (без ограничения).
	maxLen := -1
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() != vm.NullType {
		if l := int(arg(ctx, 1).ToInt()); l > 0 {
			maxLen = l
		}
	}

	var fields []string
	var field []byte
	inQuote := false
	started := false
	read := 0
	first := true
	overflow := false
	appendField := func() { fields = append(fields, string(field)); field = field[:0] }
	for {
		// Достигнут лимит длины: PHP завершает текущее поле, НО если строка
		// оборвалась внутри enclosure — дочитывает продолжение (всю
		// следующую строку) из потока и лимит больше не применяется.
		if maxLen >= 0 && read >= maxLen {
			if inQuote {
				overflow = true
			} else if !overflow {
				appendField()
				return csvArray(fields)
			}
		}
		b, ok := h.readByte()
		if !ok {
			if !started {
				return vm.Bool(false)
			}
			appendField()
			break
		}
		read++
		if first {
			first = false
			// Пустая строка (после снятия \r?\n) -> array(1){NULL}.
			if b == '\n' {
				return csvNullArray()
			}
			if b == '\r' {
				if nb, ok2 := h.readByte(); !ok2 || nb != '\n' {
					if ok2 {
						h.unreadByte(nb)
					}
				}
				return csvNullArray()
			}
		}
		started = true
		atLimit := func() bool { return maxLen >= 0 && read >= maxLen && !overflow }
		if inQuote {
			if esc != 0 && b == esc {
				field = append(field, b)
				if !atLimit() {
					if nb, ok2 := h.readByte(); ok2 {
						field = append(field, nb)
						read++
					}
				}
				continue
			}
			if b == enc {
				if atLimit() {
					// конец буфера: реальный закрывающий enclosure
					inQuote = false
					continue
				}
				nb, ok2 := h.readByte()
				if ok2 && nb == enc {
					field = append(field, enc)
					read++
					continue
				}
				if ok2 {
					h.unreadByte(nb)
				}
				inQuote = false
				continue
			}
			field = append(field, b)
			continue
		}
		switch {
		case b == enc && (len(field) == 0 || csvAllSpace(field)):
			field = field[:0]
			inQuote = true
		case b == sep:
			appendField()
		case b == '\n':
			appendField()
			return csvArray(fields)
		case b == '\r':
			nb, ok2 := h.readByte()
			if ok2 && nb != '\n' {
				h.unreadByte(nb)
			}
			appendField()
			return csvArray(fields)
		default:
			field = append(field, b)
		}
	}
	return csvArray(fields)
}

func csvNullArray() vm.Value {
	a := vm.NewArray()
	a.Append(vm.MakeNull())
	return vm.MakeArray(a)
}

func csvAllSpace(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\v', '\f':
		default:
			return false
		}
	}
	return true
}

func csvArray(fields []string) vm.Value {
	out := vm.NewArray()
	for _, f := range fields {
		out.Append(vm.MakeString(f))
	}
	return vm.MakeArray(out)
}

func fputcsvFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "fputcsv")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	fieldsSlot := arg(ctx, 1)
	if fieldsSlot.Type() != vm.ArrayType {
		return vm.Bool(false)
	}
	sep := byte(',')
	if len(ctx.Args()) >= 3 {
		s := arg(ctx, 2).ToString()
		if len(s) != 1 {
			ctx.Throw(vm.NewThrowable("ValueError", "fputcsv(): Argument #3 ($separator) must be a single character"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		if s != "" {
			sep = s[0]
		}
	}
	enc := byte('"')
	if len(ctx.Args()) >= 4 {
		s := arg(ctx, 3).ToString()
		if len(s) != 1 {
			ctx.Throw(vm.NewThrowable("ValueError", "fputcsv(): Argument #4 ($enclosure) must be a single character"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		if s != "" {
			enc = s[0]
		}
	}
	esc := byte('\\')
	if len(ctx.Args()) >= 5 {
		if s := arg(ctx, 4).ToString(); s != "" {
			esc = s[0]
		} else {
			esc = 0
		}
	}
	if esc == enc {
		esc = 0
	}
	eol := "\n"
	if len(ctx.Args()) >= 6 {
		eol = arg(ctx, 5).ToString()
	}
	var b strings.Builder
	for i, f := range fieldsSlot.AsArray().Values() {
		if i > 0 {
			b.WriteByte(sep)
		}
		s := f.ToString()
		if f.Type() == vm.ArrayType || f.Type() == vm.ObjectType {
			if sv, err := ctx.VM().ToStringValue(f); err == nil {
				s = sv
			} else {
				s = ""
			}
		}
		quote := false
		for k := 0; k < len(s); k++ {
			c := s[k]
			if c == sep || c == enc || c == '\n' || c == '\r' || c == '\t' || c == ' ' || (esc != 0 && c == esc) {
				quote = true
				break
			}
		}
		if quote {
			b.WriteByte(enc)
			for k := 0; k < len(s); k++ {
				c := s[k]
				if esc != 0 && c == esc {
					b.WriteByte(esc)
					if k+1 < len(s) {
						b.WriteByte(s[k+1])
						k++
					}
					continue
				}
				if c == enc {
					b.WriteByte(enc)
					b.WriteByte(enc)
					continue
				}
				b.WriteByte(c)
			}
			b.WriteByte(enc)
		} else {
			b.WriteString(s)
		}
	}
	b.WriteString(eol)
	n, err := h.Write([]byte(b.String()))
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Int(int64(n))
}

// --- parse_ini_file ---

func parseIniFileFn(ctx *vm.Context) vm.Value {
	fname := pathString(ctx, 0)
	if fname == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "parse_ini_file(): Argument #1 ($filename) cannot be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	resolved, ok := fsResolveIncludePath(fname, ctx)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if ok {
		fname = resolved
	}
	if !checkOpenBasedir(ctx.VM(), "parse_ini_file", fname, arg0(ctx).ToString()) {
		ctx.VM().Warning("parse_ini_file(" + arg0(ctx).ToString() + "): Failed to open stream: Operation not permitted")
		return vm.Bool(false)
	}
	data, err := os.ReadFile(fname)
	if err != nil {
		ctx.VM().Warn("parse_ini_file(" + fname + "): Failed to open stream: No such file or directory")
		return vm.Bool(false)
	}
	sections := false
	scanner := iniScannerNormal
	if len(ctx.Args()) >= 2 {
		sections = arg(ctx, 1).ToBool()
	}
	if len(ctx.Args()) >= 3 {
		scanner = int(arg(ctx, 2).ToInt())
	}
	return iniParseString(ctx, string(data), sections, scanner, fname)
}

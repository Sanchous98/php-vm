package php

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Kind — вид токена.
type Kind uint8

const (
	TEOF Kind = iota
	TIdent
	TVar   // $name
	TInt   // 123
	TFloat // 1.5
	TStr   // "..." или '...'
	TOp    // + - * / % . ( ) { } ; , = и т.п.
	TKeyword
	TInlineHTML // литеральный HTML-текст вне <?php ... ?>
)

type Token struct {
	Kind  Kind
	Val   string // текст (для TIdent/TVar/TStr/keyword/op); ключевые слова — в нижнем регистре
	Raw   string // исходный текст для TIdent/TKeyword (с сохранением регистра)
	Num   int64  // для TInt
	Flt   float64
	Pos   int
	Parts []InterpPart // для TStr с интерполяцией
	// Shell — строковый литерал в обратных кавычках (`cmd`): shell_exec.
	Shell bool
	// Attrs — сырые тексты атрибутов `#[...]`, непосредственно предшествующих
	// этому токену (без `#[` и `]`), с номером строки открывающего `#[`.
	Attrs []AttrRaw
}

// AttrRaw — сырой текст атрибута и строка его `#[` (Zend хранит атрибут с
// собственной позицией: newInstance() привязывает кадр к ней).
type AttrRaw struct {
	Text string
	Line int
}

// InterpPart — часть интерполированной строки: литерал или выражение.
type InterpPart struct {
	Text   string
	IsExpr bool
	// DollarBrace — часть вида ${...} (устаревшая форма интерполяции).
	DollarBrace bool
	// DollarBraceExpr — ${expr} с выражением (не простым именем переменной):
	// диагностика отличается (variable variables).
	DollarBraceExpr bool
	// Heredoc — часть получена из heredoc-строки (для heredoc PHP-диагностики
	// подавляются, как в `php -r`; см. parser.interp).
	Heredoc bool
}

// interpDollarBraceText строит текст для повторного разбора `${...}`:
// простая форма `${name}` разбирается как `$name`, всё остальное (напр.
// `${"y$a"}` или `${$x[0]}`) — как `${expr}` (имя переменной — значение
// выражения), чтобы парсер не требовал идентификатор сразу после `$`.
func interpDollarBraceText(inner string) (string, bool) {
	t := strings.TrimSpace(inner)
	if t != "" && isIdentStart(t[0]) {
		ok := true
		for i := 1; i < len(t); i++ {
			if !isIdentPart(t[i]) {
				ok = false
				break
			}
		}
		if ok {
			return "$" + t, false
		}
	}
	return "${" + inner + "}", true
}

var keywords = map[string]bool{
	"echo": true, "if": true, "else": true, "while": true, "for": true,
	"function": true, "return": true, "true": true, "false": true, "null": true,
	"new": true, "class": true, "foreach": true, "as": true, "break": true,
	"continue": true, "and": true, "or": true, "not": true,
	"do": true, "switch": true, "case": true, "default": true,
	"try": true, "catch": true, "finally": true, "throw": true,
	"global": true, "static": true, "unset": true, "isset": true,
	"empty": true, "list": true, "print": true, "const": true,
	"namespace": true, "use": true, "exit": true, "die": true,
	"interface": true, "trait": true, "extends": true, "implements": true,
	"abstract": true, "final": true, "public": true, "protected": true,
	"private": true, "instanceof": true, "clone": true, "xor": true,
	"array":  true,
	"elseif": true, "endif": true, "endwhile": true, "endfor": true,
	"endforeach": true, "endswitch": true, "declare": true, "enddeclare": true,
}

type Lexer struct {
	src  string
	pos  int
	toks []Token
	php  bool // true — сейчас в режиме PHP-кода, false — HTML-текст
	// braceDepth — глубина `{}` в PHP-коде: нужна для проверки, что
	// __halt_compiler() стоит на верхнем уровне скрипта.
	braceDepth int
	// pendingAttrs — тексты атрибутов, ожидающие прикрепления к следующему
	// значащему токену.
	pendingAttrs []AttrRaw
}

// emit добавляет токен, прикрепляя к нему накопленные атрибуты (кроме
// служебных HTML/EOF-токенов).
func (l *Lexer) emit(tok Token) {
	if len(l.pendingAttrs) > 0 && tok.Kind != TInlineHTML && tok.Kind != TEOF {
		tok.Attrs = l.pendingAttrs
		l.pendingAttrs = nil
	}
	l.toks = append(l.toks, tok)
}

// Lex лексирует чистый PHP-код (без открывающих тегов; закрывающий `?>`
// переключает в режим HTML и распознаётся). Используется для eval.
// HaltOffset — смещение данных после `__halt_compiler();` (byte offset в
// исходнике текущего файла); -1 — конструкция не встречалась.
var HaltOffset int64 = -1

// Lex разбирает исходник на токены (PHP-режим).
func Lex(src string) ([]Token, error) { return lexMode(src, true) }

// lexWarnings — предупреждения лексера (напр. octal overflow), привязанные к
// байтовой позиции: Parser переносит их в prog.TypeWarnings (с номером строки).
var lexWarnings []lexWarning

type lexWarning struct {
	pos int
	msg string
}

// lexMode лексирует исходник, начиная в PHP-режиме (php=true) или в режиме
// литерального HTML (php=false, смешанный PHP/HTML-файл).
func lexMode(src string, php bool) ([]Token, error) {
	lexWarnings = nil
	l := &Lexer{src: src, php: php}
	if err := l.run(); err != nil {
		return nil, err
	}
	return l.toks, nil
}

// run проходит исходник, переключаясь между HTML-текстом и PHP-кодом.
func (l *Lexer) run() error {
	if l.php {
		if err := l.runPHP(); err != nil {
			return err
		}
	}
	for l.pos < len(l.src) {
		l.scanHTML()
		if l.pos >= len(l.src) {
			break
		}
		l.openTag()
		if err := l.runPHP(); err != nil {
			return err
		}
	}
	l.emit(Token{Kind: TEOF, Pos: len(l.src)})
	return nil
}

// scanHTML накапливает литеральный HTML до ближайшего открывающего PHP-тега
// (не включая его) и выдаёт один токен TInlineHTML. При short_open_tag=off
// «короткие» теги `<?` (не `<?php`/`<?=`) остаются частью HTML.
func (l *Lexer) scanHTML() {
	start := l.pos
	for l.pos < len(l.src) {
		i := strings.Index(l.src[l.pos:], "<?")
		if i < 0 {
			l.pos = len(l.src)
			break
		}
		l.pos += i
		if isOpenTagAt(l.src, l.pos) {
			break
		}
		l.pos += 2
	}
	if l.pos > start {
		l.emit(Token{Kind: TInlineHTML, Val: l.src[start:l.pos], Pos: start})
	}
}

// ShortOpenTag управляет разбором короткого тега `<?` (настройка php.ini
// short_open_tag). `<?php` и `<?=` распознаются всегда.
var ShortOpenTag = true

// isOpenTagAt сообщает, начинается ли в позиции i допустимый открывающий тег.
func isOpenTagAt(src string, i int) bool {
	if i+1 >= len(src) || src[i] != '<' || src[i+1] != '?' {
		return false
	}
	rest := src[i:]
	if len(rest) >= 5 && strings.EqualFold(rest[:5], "<?php") {
		return true
	}
	if strings.HasPrefix(rest, "<?=") {
		return true
	}
	return ShortOpenTag
}

// openTag потребляет `<?php` (регистронезависимо), `<?=` или `<?`. Для `<?=`
// выдаёт ключевое слово echo (короткий вывод выражения).
func (l *Lexer) openTag() {
	rest := l.src[l.pos:]
	if len(rest) >= 5 && strings.EqualFold(rest[:5], "<?php") {
		l.pos += 5
		return
	}
	if strings.HasPrefix(rest, "<?=") {
		l.emit(Token{Kind: TKeyword, Val: "echo", Pos: l.pos})
		l.pos += 3
		return
	}
	if strings.HasPrefix(rest, "<?") {
		l.pos += 2
		return
	}
}

// runPHP лексирует PHP-код до `?>` (или конца исходника). Закрывающий тег
// возвращает управление в HTML-режим; следующий за `?>` перевод строки
// «съедается» (как в PHP).
func (l *Lexer) runPHP() error {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '?' && l.peek(1) == '>' {
			// Закрывающий тег действует как завершение оператора (`;`).
			l.emit(Token{Kind: TOp, Val: ";", Pos: l.pos})
			l.pos += 2
			if l.pos < len(l.src) && l.src[l.pos] == '\n' {
				l.pos++
			} else if l.pos+1 < len(l.src) && l.src[l.pos] == '\r' && l.src[l.pos+1] == '\n' {
				l.pos += 2
			}
			return nil
		}
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			l.pos++
		case c == '/' && l.peek(1) == '/':
			// Однострочный комментарий: завершается переводом строки или
			// закрывающим тегом `?>` (как в PHP).
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				if l.src[l.pos] == '?' && l.peek(1) == '>' {
					break
				}
				l.pos++
			}
		case c == '#':
			if l.peek(1) == '[' {
				// Атрибут `#[...]`: сохраняем сырой текст (без `#[` и `]`),
				// строку открывающего `#[` и прикрепляем к следующему
				// значащему токену.
				attrLine := 1 + strings.Count(l.src[:l.pos], "\n")
				innerStart := l.pos + 2
				l.pos += 2
				depth := 1
				for l.pos < len(l.src) && depth > 0 {
					ch := l.src[l.pos]
					switch ch {
					case '[':
						depth++
						l.pos++
					case ']':
						depth--
						if depth == 0 {
							l.pendingAttrs = append(l.pendingAttrs, AttrRaw{Text: l.src[innerStart:l.pos], Line: attrLine})
						}
						l.pos++
					case '"', '\'':
						q := ch
						l.pos++
						for l.pos < len(l.src) && l.src[l.pos] != q {
							if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
								l.pos++
							}
							l.pos++
						}
						if l.pos < len(l.src) {
							l.pos++
						}
					default:
						l.pos++
					}
				}
				continue
			}
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				if l.src[l.pos] == '?' && l.peek(1) == '>' {
					break
				}
				l.pos++
			}
		case c == '/' && l.peek(1) == '*':
			commentLine := 1 + strings.Count(l.src[:l.pos], "\n")
			l.pos += 2
			for l.pos < len(l.src) && !(l.src[l.pos] == '*' && l.peek(1) == '/') {
				l.pos++
			}
			if l.pos >= len(l.src) {
				// Незакрытый блочный комментарий: Zend сообщает номер строки
				// его начала.
				return l.parseErrf("Unterminated comment starting line %d", commentLine)
			}
			l.pos += 2
		case c == '$':
			start := l.pos
			l.pos++
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			l.emit(Token{Kind: TVar, Val: l.src[start:l.pos], Pos: start})
		case c >= '0' && c <= '9':
			if err := l.number(); err != nil {
				return err
			}
		case c == '.' && l.peek(1) >= '0' && l.peek(1) <= '9':
			if err := l.number(); err != nil {
				return err
			}
		case c == '"' || c == '\'' || c == '`':
			if err := l.string(c); err != nil {
				return err
			}
		case c == '<' && l.peek(1) == '<' && l.peek(2) == '<':
			if err := l.heredoc(); err != nil {
				return err
			}
		case isIdentStart(c):
			start := l.pos
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			w := l.src[start:l.pos]
			// __halt_compiler(): всё после него — данные, не разбираем.
			if strings.EqualFold(w, "__halt_compiler") {
				// Zend допускает конструкцию только на верхнем уровне
				// скрипта (bug75218, halt03).
				if l.braceDepth > 0 {
					return &fatalError{msg: "__HALT_COMPILER() can only be used from the outermost scope", line: 1 + strings.Count(l.src[:start], "\n")}
				}
				j := l.pos
				for j < len(l.src) && isSpaceByte(l.src[j]) {
					j++
				}
				if j < len(l.src) && l.src[j] == '(' {
					depth := 0
					for j < len(l.src) {
						if l.src[j] == '(' {
							depth++
						} else if l.src[j] == ')' {
							depth--
							if depth == 0 {
								j++
								break
							}
						}
						j++
					}
					// Смещение данных — сразу после `;` (как
					// zend_get_scanned_file_offset после редукции).
					off := j
					for off < len(l.src) && isSpaceByte(l.src[off]) {
						off++
					}
					if off < len(l.src) && l.src[off] == ';' {
						off++
					}
					HaltOffset = int64(off)
					l.pos = len(l.src)
					return nil
				}
			}
			k := TIdent
			if lw := strings.ToLower(w); keywords[lw] {
				k = TKeyword
				raw := w
				w = lw
				l.emit(Token{Kind: k, Val: w, Raw: raw, Pos: start})
				continue
			}
			// Бинарный строковый литерал: b"..." / b'...' (префикс b/B
			// непосредственно перед кавычкой).
			if (w == "b" || w == "B") && k == TIdent && l.pos < len(l.src) &&
				(l.src[l.pos] == '"' || l.src[l.pos] == '\'') {
				if err := l.string(l.src[l.pos]); err != nil {
					return err
				}
				continue
			}
			// Бинарный heredoc: b<<<LABEL.
			if (w == "b" || w == "B") && k == TIdent && l.pos+2 < len(l.src) &&
				l.src[l.pos] == '<' && l.src[l.pos+1] == '<' && l.src[l.pos+2] == '<' {
				if err := l.heredoc(); err != nil {
					return err
				}
				continue
			}
			l.emit(Token{Kind: k, Val: w, Raw: w, Pos: start})
		default:
			start := l.pos
			op := l.operator()
			if op == "" {
				return l.parseErrf("syntax error, unexpected character 0x%02X", c)
			}
			if op == "{" {
				l.braceDepth++
			} else if op == "}" && l.braceDepth > 0 {
				l.braceDepth--
			}
			l.emit(Token{Kind: TOp, Val: op, Pos: start})
		}
	}
	return nil
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func (l *Lexer) peek(n int) byte {
	if l.pos+n < len(l.src) {
		return l.src[l.pos+n]
	}
	return 0
}

func (l *Lexer) number() error {
	start := l.pos
	// --- целочисленные литералы с основанием: 0x.., 0b.., 0o.., 0<octal> ---
	if l.src[l.pos] == '0' && l.pos+1 < len(l.src) {
		switch next := l.src[l.pos+1]; {
		case next == 'x' || next == 'X':
			return l.basedNumber(start, 16, 2, isHexDigit)
		case next == 'b' || next == 'B':
			return l.basedNumber(start, 2, 2, func(c byte) bool { return c == '0' || c == '1' })
		case next == 'o' || next == 'O':
			return l.basedNumber(start, 8, 2, func(c byte) bool { return c >= '0' && c <= '7' })
		case next >= '0' && next <= '7' || next == '_' && l.pos+2 < len(l.src) && l.src[l.pos+2] >= '0' && l.src[l.pos+2] <= '7':
			// legacy octal: 0[0-7_]+ (в т.ч. 0_124). Если дальше идёт
			// точка/экспонента — это десятичный литерал (01.5, 010e2).
			j := l.pos + 1
			for j < len(l.src) {
				c := l.src[j]
				if c >= '0' && c <= '9' {
					j++
					continue
				}
				if c == '_' && j+1 < len(l.src) && l.src[j+1] >= '0' && l.src[j+1] <= '9' {
					j++
					continue
				}
				break
			}
			if j < len(l.src) && (l.src[j] == '.' || l.src[j] == 'e' || l.src[j] == 'E') {
				break
			}
			l.pos = j
			txt := strings.ReplaceAll(l.src[start:l.pos], "_", "")
			l.emitBased(txt, 8, start)
			return nil
		}
	}
	isFloat := false
	l.scanDigits()
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		isFloat = true
		l.pos++
		// Первая цифра дробной части не может быть разделителем (`100._0`).
		if l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			l.scanDigits()
		}
	}
	if l.pos < len(l.src) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
		j := l.pos + 1
		if j < len(l.src) && (l.src[j] == '+' || l.src[j] == '-') {
			j++
		}
		if j < len(l.src) && l.src[j] >= '0' && l.src[j] <= '9' {
			isFloat = true
			l.pos = j
			l.scanDigits()
		}
	}
	txt := strings.ReplaceAll(l.src[start:l.pos], "_", "")
	if !isFloat && len(txt) > 1 && txt[0] == '0' {
		// десятичный литерал с ведущим нулём, содержащий 8/9 (напр. 08) —
		// в PHP это ошибка разбора "Invalid numeric literal".
		for i := 1; i < len(txt); i++ {
			if txt[i] == '8' || txt[i] == '9' {
				return l.parseErrf("Invalid numeric literal")
			}
		}
	}
	if isFloat {
		f, _ := strconv.ParseFloat(txt, 64)
		l.emit(Token{Kind: TFloat, Flt: f, Val: txt, Pos: start})
		return nil
	}
	i, err := strconv.ParseInt(txt, 10, 64)
	if err != nil {
		// слишком большое целое -> float (как PHP)
		f, _ := strconv.ParseFloat(txt, 64)
		l.emit(Token{Kind: TFloat, Flt: f, Val: txt, Pos: start})
		return nil
	}
	l.emit(Token{Kind: TInt, Num: i, Val: txt, Pos: start})
	return nil
}

// basedNumber лексирует число с префиксом (0x/0b/0o) и данным основанием.
func (l *Lexer) basedNumber(start, base int, prefixLen int, ok func(byte) bool) error {
	l.pos += prefixLen
	// Первый символ после префикса обязан быть цифрой: `0x_0123` — это `0`
	// и идентификатор `x_0123`, а не литерал.
	if l.pos >= len(l.src) || !ok(l.src[l.pos]) {
		l.pos = start + 1
		l.emit(Token{Kind: TInt, Num: 0, Val: "0", Pos: start})
		return nil
	}
	ds := l.pos
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if ok(c) {
			l.pos++
			continue
		}
		if c == '_' && l.pos+1 < len(l.src) && ok(l.src[l.pos+1]) {
			l.pos++
			continue
		}
		break
	}
	if l.pos == ds {
		// нет цифр — не литерал; вернём "0" и оставим остальное как есть
		l.pos = start + 1
		l.emit(Token{Kind: TInt, Num: 0, Val: "0", Pos: start})
		return nil
	}
	txt := strings.ReplaceAll(l.src[ds:l.pos], "_", "")
	l.emitBased(txt, base, start)
	return nil
}

// emitBased разбирает число в заданном основании. При выходе за int64 PHP
// превращает литерал во float.
func (l *Lexer) emitBased(txt string, base int, start int) {
	v, ok := new(big.Int).SetString(txt, base)
	if !ok {
		l.emit(Token{Kind: TInt, Num: 0, Val: "0", Pos: start})
		return
	}
	if v.IsInt64() {
		l.emit(Token{Kind: TInt, Num: v.Int64(), Val: txt, Pos: start})
		return
	}
	f, _ := new(big.Float).SetInt(v).Float64()
	l.emit(Token{Kind: TFloat, Flt: f, Val: txt, Pos: start})
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func digitOrSep(c byte) bool {
	return (c >= '0' && c <= '9') || c == '_'
}

// scanDigits продвигает l.pos по цифрам; '_' допускается только между цифрами
// (Zend: 100_ — это литерал 100 и следующий идентификатор "_").
func (l *Lexer) scanDigits() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c >= '0' && c <= '9' {
			l.pos++
			continue
		}
		if c == '_' && l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9' {
			l.pos++
			continue
		}
		break
	}
}

func (l *Lexer) string(q byte) error {
	start := l.pos
	l.pos++ // открывающая кавычка
	if q == '\'' {
		var b strings.Builder
		for l.pos < len(l.src) && l.src[l.pos] != q {
			if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) && (l.src[l.pos+1] == '\'' || l.src[l.pos+1] == '\\') {
				l.pos++
				b.WriteByte(l.src[l.pos])
				l.pos++
				continue
			}
			b.WriteByte(l.src[l.pos])
			l.pos++
		}
		if l.pos >= len(l.src) {
			return fmt.Errorf("lexer: незакрытая строка на позиции %d", start)
		}
		l.pos++
		l.emit(Token{Kind: TStr, Val: b.String(), Pos: start})
		return nil
	}

	// двойные кавычки: интерполяция
	var lit strings.Builder
	var parts []InterpPart
	hasExpr := false
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, InterpPart{Text: lit.String()})
			lit.Reset()
		}
	}
	for l.pos < len(l.src) && l.src[l.pos] != q {
		c := l.src[l.pos]
		if c == '\\' && l.pos+1 < len(l.src) {
			l.pos++
			if l.src[l.pos] == 'u' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '{' {
				out, nx, uerr := decodeUEscape(l.src, l.pos)
				if uerr != "" {
					return l.parseErrf("%s", uerr)
				}
				lit.WriteString(out)
				l.pos = nx
				continue
			}
			switch l.src[l.pos] {
			case 'n':
				lit.WriteByte('\n')
			case 't':
				lit.WriteByte('\t')
			case 'r':
				lit.WriteByte('\r')
			case 'e':
				lit.WriteByte(0x1B)
			case 'f':
				lit.WriteByte(0x0C)
			case 'v':
				lit.WriteByte(0x0B)
			case 'x':
				if l.pos+1 < len(l.src) && isHexDigit(l.src[l.pos+1]) {
					l.pos++
					v := hexVal(l.src[l.pos])
					if l.pos+1 < len(l.src) && isHexDigit(l.src[l.pos+1]) {
						l.pos++
						v = v*16 + hexVal(l.src[l.pos])
					}
					lit.WriteByte(byte(v))
				} else {
					lit.WriteByte('\\')
					lit.WriteByte('x')
				}
			case '0', '1', '2', '3', '4', '5', '6', '7':
				start := l.pos
				v := int(l.src[l.pos] - '0')
				for n := 0; n < 2 && l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '7'; n++ {
					l.pos++
					v = v*8 + int(l.src[l.pos]-'0')
				}
				if v > 0xFF {
					// Zend: \NNN > \377 — предупреждение, значение по модулю 256.
					lexWarnings = append(lexWarnings, lexWarning{
						pos: start - 1,
						msg: fmt.Sprintf("Octal escape sequence overflow \\%o is greater than \\377", v),
					})
				}
				lit.WriteByte(byte(v))
			case '\\':
				lit.WriteByte('\\')
			case '$':
				lit.WriteByte('$')
			case '"':
				lit.WriteByte('"')
			default:
				lit.WriteByte('\\')
				lit.WriteByte(l.src[l.pos])
			}
			l.pos++
			continue
		}
		if c == '$' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '{' {
			flush()
			depth := 0
			j := l.pos + 1
			for j < len(l.src) {
				if l.src[j] == '{' {
					depth++
				} else if l.src[j] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
				j++
			}
			if j >= len(l.src) {
				return fmt.Errorf("lexer: незакрытая интерполяция на позиции %d", l.pos)
			}
			inner := l.src[l.pos+2 : j]
			txt, exprForm := interpDollarBraceText(inner)
			parts = append(parts, InterpPart{Text: txt, IsExpr: true, DollarBrace: true, DollarBraceExpr: exprForm})
			hasExpr = true
			l.pos = j + 1
			continue
		}
		if c == '{' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '$' {
			flush()
			depth := 0
			j := l.pos
			for j < len(l.src) {
				if l.src[j] == '{' {
					depth++
				} else if l.src[j] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
				j++
			}
			if j >= len(l.src) {
				return fmt.Errorf("lexer: незакрытая интерполяция на позиции %d", l.pos)
			}
			inner := l.src[l.pos+1 : j]
			parts = append(parts, InterpPart{Text: inner, IsExpr: true})
			hasExpr = true
			l.pos = j + 1
			continue
		}
		if c == '$' && l.pos+1 < len(l.src) && isIdentStart(l.src[l.pos+1]) {
			flush()
			vs := l.pos
			l.pos++ // $
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			for l.pos < len(l.src) {
				if l.src[l.pos] == '[' {
					// Простая интерполяция `$name[...]`: PHP допускает
					// только голый идентификатор, число, `-` или `$var`.
					// Строковый литерал/пробел внутри скобок — parse error.
					if l.pos+1 >= len(l.src) {
						return l.parseErrf("syntax error, unexpected end of file, expecting \"-\" or identifier or variable or number")
					}
					n := l.src[l.pos+1]
					if !(n == '$' || n == '-' || isIdentStart(n) || (n >= '0' && n <= '9')) {
						return l.parseErrf("syntax error, unexpected string content %q, expecting \"-\" or identifier or variable or number", "")
					}
					depth := 0
					j := l.pos
					for j < len(l.src) {
						if l.src[j] == '[' {
							depth++
						} else if l.src[j] == ']' {
							depth--
							if depth == 0 {
								j++
								break
							}
						}
						j++
					}
					l.pos = j
					continue
				}
				if l.src[l.pos] == '-' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
					// `->name` — часть простой интерполяции только если за
					// `->` идёт имя свойства; иначе `->` — литеральный текст
					// (PHP: "$obj-> " интерполирует только $obj).
					np := l.pos + 2
					if np >= len(l.src) || !(isIdentStart(l.src[np]) || l.src[np] == '$') {
						break
					}
					l.pos = np
					for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
						l.pos++
					}
					continue
				}
				if l.src[l.pos] == '?' && l.pos+2 < len(l.src) && l.src[l.pos+1] == '-' && l.src[l.pos+2] == '>' {
					// `?->name` — nullsafe-доступ в простой интерполяции.
					np := l.pos + 3
					if np >= len(l.src) || !(isIdentStart(l.src[np]) || l.src[np] == '$') {
						break
					}
					l.pos = np
					for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
						l.pos++
					}
					continue
				}
				break
			}
			parts = append(parts, InterpPart{Text: l.src[vs:l.pos], IsExpr: true})
			hasExpr = true
			continue
		}
		lit.WriteByte(c)
		l.pos++
	}
	if l.pos >= len(l.src) {
		return fmt.Errorf("lexer: незакрытая строка на позиции %d", start)
	}
	l.pos++ // закрывающая кавычка
	if !hasExpr {
		l.emit(Token{Kind: TStr, Val: lit.String(), Pos: start, Shell: q == '`'})
		return nil
	}
	flush()
	l.emit(Token{Kind: TStr, Val: lit.String(), Parts: parts, Pos: start, Shell: q == '`'})
	return nil
}

// operator — самый длинный подходящий оператор/пункт.
func (l *Lexer) operator() string {
	for _, op := range []string{
		"...", "?->",
		"===", "!==", "<=>", "**=", "<<=", ">>=", "??=", "??",
		"&&", "||", "->", "::", "=>",
		"++", "--", "+=", "-=", "*=", "/=", ".=", "%=",
		"&=", "|=", "^=", "**", "<<", ">>", "==", "!=", "<=", ">=", "<>",
		"+", "-", "*", "/", "%", ".", "(", ")", "{", "}", "[", "]",
		";", ",", "=", "<", ">", "!", "&", "|", "^", "~",
		"?", ":", "@", "\\",
	} {
		if strings.HasPrefix(l.src[l.pos:], op) {
			l.pos += len(op)
			return op
		}
	}
	return ""
}

func isIdentStart(c byte) bool {
	// PHP допускает байты >= 0x80 в идентификаторах (символы вне ASCII
	// трактуются как обычные буквы, независимо от кодировки).
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// interp сканирует тело интерполированной строки от l.pos. Останавливается,
// когда stop() истинно (терминатор не потребляется). escapes — обрабатывать
// escape-последовательности (как в "..." и heredoc). Используется для heredoc.
func (l *Lexer) interp(escapes bool, stop func() bool) (string, []InterpPart, bool, error) {
	var lit strings.Builder
	var parts []InterpPart
	hasExpr := false
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, InterpPart{Text: lit.String()})
			lit.Reset()
		}
	}
	for l.pos < len(l.src) && !stop() {
		c := l.src[l.pos]
		if escapes && c == '\\' && l.pos+1 < len(l.src) {
			l.pos++
			if l.src[l.pos] == 'u' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '{' {
				out, nx, uerr := decodeUEscape(l.src, l.pos)
				if uerr != "" {
					return "", nil, false, l.parseErrf("%s", uerr)
				}
				lit.WriteString(out)
				l.pos = nx
				continue
			}
			switch l.src[l.pos] {
			case 'n':
				lit.WriteByte('\n')
			case 't':
				lit.WriteByte('\t')
			case 'r':
				lit.WriteByte('\r')
			case 'e':
				lit.WriteByte(0x1B)
			case 'f':
				lit.WriteByte(0x0C)
			case 'v':
				lit.WriteByte(0x0B)
			case 'x':
				if l.pos+1 < len(l.src) && isHexDigit(l.src[l.pos+1]) {
					l.pos++
					v := hexVal(l.src[l.pos])
					if l.pos+1 < len(l.src) && isHexDigit(l.src[l.pos+1]) {
						l.pos++
						v = v*16 + hexVal(l.src[l.pos])
					}
					lit.WriteByte(byte(v))
				} else {
					lit.WriteByte('\\')
					lit.WriteByte('x')
				}
			case '0', '1', '2', '3', '4', '5', '6', '7':
				start := l.pos
				v := int(l.src[l.pos] - '0')
				for n := 0; n < 2 && l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '7'; n++ {
					l.pos++
					v = v*8 + int(l.src[l.pos]-'0')
				}
				if v > 0xFF {
					// Zend: \NNN > \377 — предупреждение, значение по модулю 256.
					lexWarnings = append(lexWarnings, lexWarning{
						pos: start - 1,
						msg: fmt.Sprintf("Octal escape sequence overflow \\%o is greater than \\377", v),
					})
				}
				lit.WriteByte(byte(v))
			case '\\':
				lit.WriteByte('\\')
			case '$':
				lit.WriteByte('$')
			default:
				lit.WriteByte('\\')
				lit.WriteByte(l.src[l.pos])
			}
			l.pos++
			continue
		}
		if c == '$' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '{' {
			flush()
			depth := 0
			j := l.pos + 1
			for j < len(l.src) {
				if l.src[j] == '{' {
					depth++
				} else if l.src[j] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
				j++
			}
			if j >= len(l.src) {
				return "", nil, false, fmt.Errorf("lexer: незакрытая интерполяция на позиции %d", l.pos)
			}
			inner := l.src[l.pos+2 : j]
			txt, exprForm := interpDollarBraceText(inner)
			parts = append(parts, InterpPart{Text: txt, IsExpr: true, DollarBrace: true, DollarBraceExpr: exprForm})
			hasExpr = true
			l.pos = j + 1
			continue
		}
		if c == '{' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '$' {
			flush()
			depth := 0
			j := l.pos
			for j < len(l.src) {
				if l.src[j] == '{' {
					depth++
				} else if l.src[j] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
				j++
			}
			if j >= len(l.src) {
				return "", nil, false, fmt.Errorf("lexer: незакрытая интерполяция на позиции %d", l.pos)
			}
			inner := l.src[l.pos+1 : j]
			parts = append(parts, InterpPart{Text: inner, IsExpr: true})
			hasExpr = true
			l.pos = j + 1
			continue
		}
		if c == '$' && l.pos+1 < len(l.src) && isIdentStart(l.src[l.pos+1]) {
			flush()
			vs := l.pos
			l.pos++
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			for l.pos < len(l.src) {
				if l.src[l.pos] == '[' {
					// Простая интерполяция `$name[...]`: PHP допускает
					// только голый идентификатор, число, `-` или `$var`.
					// Строковый литерал/пробел внутри скобок — parse error.
					if l.pos+1 >= len(l.src) {
						return "", nil, false, l.parseErrf("syntax error, unexpected end of file, expecting \"-\" or identifier or variable or number")
					}
					n := l.src[l.pos+1]
					if !(n == '$' || n == '-' || isIdentStart(n) || (n >= '0' && n <= '9')) {
						return "", nil, false, l.parseErrf("syntax error, unexpected string content %q, expecting \"-\" or identifier or variable or number", "")
					}
					depth := 0
					j := l.pos
					for j < len(l.src) {
						if l.src[j] == '[' {
							depth++
						} else if l.src[j] == ']' {
							depth--
							if depth == 0 {
								j++
								break
							}
						}
						j++
					}
					l.pos = j
					continue
				}
				if l.src[l.pos] == '-' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
					// `->name` — часть простой интерполяции только если за
					// `->` идёт имя свойства; иначе `->` — литеральный текст
					// (PHP: "$obj-> " интерполирует только $obj).
					np := l.pos + 2
					if np >= len(l.src) || !(isIdentStart(l.src[np]) || l.src[np] == '$') {
						break
					}
					l.pos = np
					for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
						l.pos++
					}
					continue
				}
				if l.src[l.pos] == '?' && l.pos+2 < len(l.src) && l.src[l.pos+1] == '-' && l.src[l.pos+2] == '>' {
					// `?->name` — nullsafe-доступ в простой интерполяции.
					np := l.pos + 3
					if np >= len(l.src) || !(isIdentStart(l.src[np]) || l.src[np] == '$') {
						break
					}
					l.pos = np
					for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
						l.pos++
					}
					continue
				}
				break
			}
			parts = append(parts, InterpPart{Text: l.src[vs:l.pos], IsExpr: true})
			hasExpr = true
			continue
		}
		lit.WriteByte(c)
		l.pos++
	}
	if !hasExpr {
		return lit.String(), nil, false, nil
	}
	flush()
	for i := range parts {
		parts[i].Heredoc = true
	}
	return lit.String(), parts, true, nil
}

// heredoc лексирует <<<LABEL ... LABEL, <<<"LABEL" ... (интерполяция) и
// <<<'LABEL' ... (nowdoc, без интерполяции). Поддерживает отступ закрывающего
// маркера (PHP 7.3+).
func (l *Lexer) heredoc() error {
	start := l.pos
	l.pos += 3 // <<<
	for l.pos < len(l.src) && (l.src[l.pos] == ' ' || l.src[l.pos] == '\t') {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return fmt.Errorf("lexer: незакрытый heredoc на позиции %d", start)
	}
	nowdoc := false
	var label string
	switch l.src[l.pos] {
	case '\'':
		l.pos++
		s := l.pos
		for l.pos < len(l.src) && l.src[l.pos] != '\'' {
			l.pos++
		}
		if l.pos >= len(l.src) {
			return fmt.Errorf("lexer: незакрытый heredoc на позиции %d", start)
		}
		label = l.src[s:l.pos]
		l.pos++
		nowdoc = true
	case '"':
		l.pos++
		s := l.pos
		for l.pos < len(l.src) && l.src[l.pos] != '"' {
			l.pos++
		}
		if l.pos >= len(l.src) {
			return fmt.Errorf("lexer: незакрытый heredoc на позиции %d", start)
		}
		label = l.src[s:l.pos]
		l.pos++
	default:
		s := l.pos
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
		label = l.src[s:l.pos]
	}
	if label == "" {
		return fmt.Errorf("lexer: пустая метка heredoc на позиции %d", start)
	}
	for l.pos < len(l.src) && (l.src[l.pos] == ' ' || l.src[l.pos] == '\t') {
		l.pos++
	}
	// Перевод строки после открывающей метки: \n, \r\n или \r (bug79934).
	switch {
	case l.pos < len(l.src) && l.src[l.pos] == '\r':
		l.pos++
		if l.pos < len(l.src) && l.src[l.pos] == '\n' {
			l.pos++
		}
	case l.pos < len(l.src) && l.src[l.pos] == '\n':
		l.pos++
	default:
		return fmt.Errorf("lexer: heredoc: ожидался перевод строки на позиции %d", l.pos)
	}
	contentStart := l.pos

	lineStart := contentStart
	closing := -1
	labelEnd := 0
	indent := ""
	for lineStart <= len(l.src) {
		q := lineStart
		for q < len(l.src) && (l.src[q] == ' ' || l.src[q] == '\t') {
			q++
		}
		if q+len(label) <= len(l.src) && l.src[q:q+len(label)] == label {
			after := q + len(label)
			if after >= len(l.src) || !isIdentPart(l.src[after]) {
				closing = lineStart
				labelEnd = after
				indent = l.src[lineStart:q]
				break
			}
		}
		_, next := nextLineBreak(l.src, lineStart)
		if next <= lineStart {
			break
		}
		lineStart = next
	}
	if closing < 0 {
		if contentStart >= len(l.src) {
			// Тело не началось: Zend сообщает короткую ошибку.
			return &parseError{msg: "syntax error, unexpected end of file", line: lineOf(l.src, len(l.src))}
		}
		return &parseError{msg: `syntax error, unexpected end of file, expecting variable or heredoc end or "${" or "{$"`, line: lineOf(l.src, len(l.src))}
	}
	// Валидация отступов гибкого heredoc (Zend).
	if mixed := mixedIndent(indent); mixed {
		return &parseError{msg: "Invalid indentation - tabs and spaces cannot be mixed", line: lineOf(l.src, closing)}
	}
	body := l.src[contentStart:closing]
	if indent != "" {
		if err := checkHeredocBodyIndent(l.src, contentStart, closing, indent); err != nil {
			return err
		}
	}
	body = strings.TrimSuffix(body, "\n")
	body = strings.TrimSuffix(body, "\r")
	if indent != "" {
		body = stripHeredocIndent(body, indent)
	}
	l.pos = labelEnd

	if nowdoc {
		l.emit(Token{Kind: TStr, Val: body, Pos: start})
		return nil
	}
	tmp := &Lexer{src: body}
	val, parts, hasExpr, err := tmp.interp(true, func() bool { return tmp.pos >= len(tmp.src) })
	if err != nil {
		return err
	}
	if !hasExpr {
		l.emit(Token{Kind: TStr, Val: val, Pos: start})
		return nil
	}
	l.emit(Token{Kind: TStr, Val: val, Parts: parts, Pos: start})
	return nil
}

// mixedIndent сообщает, что в отступе смешаны пробелы и табы.
func mixedIndent(s string) bool {
	hasSpace := strings.ContainsRune(s, ' ')
	hasTab := strings.ContainsRune(s, '\t')
	return hasSpace && hasTab
}

// nextLineBreak возвращает (конец содержимого строки, начало следующей):
// переводы строк — \n, \r\n и \r (как в Zend).
func nextLineBreak(src string, from int) (int, int) {
	for j := from; j < len(src); j++ {
		switch src[j] {
		case '\n':
			return j, j + 1
		case '\r':
			if j+1 < len(src) && src[j+1] == '\n' {
				return j, j + 2
			}
			return j, j + 1
		}
	}
	return len(src), len(src) + 1
}

// checkHeredocBodyIndent проверяет отступы строк тела гибкого heredoc
// (повторяет strip_multiline_string_indentation из Zend).
func checkHeredocBodyIndent(src string, from, to int, indent string) error {
	usingSpaces := !strings.ContainsRune(indent, '\t')
	i := from
	for i < to {
		nl, next := nextLineBreak(src, i)
		if nl > to {
			nl = to
		}
		pos := i
		for skip := 0; skip < len(indent); skip++ {
			if pos >= nl {
				// Строка только из пробелов (или пустая, в т.ч. CRLF):
				// неполный отступ допустим.
				break
			}
			if pos >= to {
				break
			}
			ch := src[pos]
			if ch != ' ' && ch != '\t' {
				return &parseError{msg: fmt.Sprintf("Invalid body indentation level (expecting an indentation level of at least %d)", len(indent)), line: lineOf(src, i)}
			}
			if (!usingSpaces && ch == ' ') || (usingSpaces && ch == '\t') {
				return &parseError{msg: "Invalid indentation - tabs and spaces cannot be mixed", line: lineOf(src, i)}
			}
			pos++
		}
		if next > to {
			break
		}
		i = next
	}
	return nil
}

// decodeUEscape разбирает \u{...} (src[i]=='u', src[i+1]=='{'). Возвращает
// декодированные байты (UTF-8, включая CESU-8-кодирование суррогатов) и позицию
// за '}'. ok == false при некорректной последовательности.
func decodeUEscape(src string, i int) (string, int, string) {
	if i+1 >= len(src) || src[i] != 'u' || src[i+1] != '{' {
		return "", i, "Invalid UTF-8 codepoint escape sequence"
	}
	j := i + 2
	start := j
	var v uint32
	for j < len(src) && src[j] != '}' {
		c := src[j]
		var d uint32
		switch {
		case c >= '0' && c <= '9':
			d = uint32(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint32(c-'A') + 10
		default:
			return "", i, "Invalid UTF-8 codepoint escape sequence"
		}
		v = v*16 + d
		if v > 0x10FFFF {
			return "", i, "Invalid UTF-8 codepoint escape sequence: Codepoint too large"
		}
		j++
	}
	if j >= len(src) || src[j] != '}' || j == start {
		return "", i, "Invalid UTF-8 codepoint escape sequence"
	}
	return encodeCodePoint(v), j + 1, ""
}

// parseErrf строит PHP-совместимую ошибку разбора ("Parse error: <msg> in ...").
func (l *Lexer) parseErrf(format string, args ...any) error {
	return &parseError{msg: fmt.Sprintf(format, args...), line: lineOf(l.src, l.pos)}
}

// encodeCodePoint кодирует код-поинт в UTF-8-подобную последовательность, в том
// числе CESU-8 для суррогатных половин (PHP допускает \u{D800}).
func encodeCodePoint(v uint32) string {
	var b [4]byte
	switch {
	case v < 0x80:
		return string([]byte{byte(v)})
	case v < 0x800:
		b[0] = 0xC0 | byte(v>>6)
		b[1] = 0x80 | byte(v&0x3F)
		return string(b[:2])
	case v < 0x10000:
		b[0] = 0xE0 | byte(v>>12)
		b[1] = 0x80 | byte((v>>6)&0x3F)
		b[2] = 0x80 | byte(v&0x3F)
		return string(b[:3])
	default:
		b[0] = 0xF0 | byte(v>>18)
		b[1] = 0x80 | byte((v>>12)&0x3F)
		b[2] = 0x80 | byte((v>>6)&0x3F)
		b[3] = 0x80 | byte(v&0x3F)
		return string(b[:4])
	}
}

// stripHeredocIndent убирает не более len(indent) ведущих пробелов/табов в
// каждой строке тела heredoc (flexible heredoc, PHP 7.3+).
func stripHeredocIndent(body, indent string) string {
	if body == "" {
		return body
	}
	n := len(indent)
	var b strings.Builder
	b.Grow(len(body))
	i := 0
	for i < len(body) {
		nl, next := nextLineBreak(body, i)
		if nl > len(body) {
			nl = len(body)
		}
		ln := body[i:nl]
		k := 0
		for k < n && k < len(ln) && (ln[k] == ' ' || ln[k] == '\t') {
			k++
		}
		b.WriteString(ln[k:])
		if nl >= len(body) {
			break
		}
		b.WriteString(body[nl:next])
		i = next
	}
	return b.String()
}

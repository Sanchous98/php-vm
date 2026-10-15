package php

// Реализация htmlspecialchars/htmlentities/html_entity_decode по мотивам
// php-src ext/standard/html.c (PHP 8.3): точные правила допустимых символов
// для каждого doctype, таблицы кодировок и многобайтовые последовательности.

import (
	"strconv"
	"strings"
)

// --- определение кодировки (charset_map из php-src) ---

var htmlCharsetMap = map[string]string{
	"iso-8859-1": "ISO-8859-1", "iso8859-1": "ISO-8859-1",
	"iso-8859-15": "ISO-8859-15", "iso8859-15": "ISO-8859-15",
	"utf-8":  "UTF-8",
	"cp1252": "Windows-1252", "windows-1252": "Windows-1252", "1252": "Windows-1252",
	"big5": "BIG5", "950": "BIG5",
	"gb2312": "GB2312", "936": "GB2312",
	"big5-hkscs": "BIG5-HKSCS",
	"shift_jis":  "SJIS", "sjis": "SJIS", "932": "SJIS", "sjis-win": "SJIS", "cp932": "SJIS",
	"eucjp": "EUC-JP", "euc-jp": "EUC-JP", "eucjp-win": "EUC-JP",
	"koi8-r": "KOI8-R", "koi8-ru": "KOI8-R", "koi8r": "KOI8-R",
	"cp1251": "CP1251", "windows-1251": "CP1251", "win-1251": "CP1251",
	"iso8859-5": "ISO-8859-5", "iso-8859-5": "ISO-8859-5",
	"cp866": "CP866", "866": "CP866", "ibm866": "CP866",
	"macroman": "MacRoman",
}

func htmlCanonCharset(name string) (string, bool) {
	c, ok := htmlCharsetMap[strings.ToLower(name)]
	return c, ok
}

// htmlDefaultCharset повторяет get_default_charset(): ini internal_encoding,
// затем default_charset. mbstring.internal_encoding намеренно не используется.
func htmlDefaultCharset() string {
	if v, ok := IniValue("internal_encoding"); ok && v != "" {
		if c, ok2 := htmlCanonCharset(v); ok2 {
			return c
		}
		return v
	}
	if v, ok := IniValue("default_charset"); ok && v != "" {
		if c, ok2 := htmlCanonCharset(v); ok2 {
			return c
		}
		return v
	}
	return "UTF-8"
}

func htmlCharsetUnicodeCompat(cs string) bool { return cs == "UTF-8" || cs == "ISO-8859-1" }
func htmlCharsetSingleByte(cs string) bool {
	switch cs {
	case "Windows-1252", "ISO-8859-15", "CP1251", "ISO-8859-5", "CP866", "MacRoman", "KOI8-R":
		return true
	}
	return false
}
func htmlCharsetPartial(cs string) bool {
	switch cs {
	case "BIG5", "BIG5-HKSCS", "GB2312", "SJIS", "EUC-JP":
		return true
	}
	return false
}

// --- map_from_unicode: кодпойнт -> байт в целевой кодировке ---

func htmlMapFromUnicode(code rune, cs string) (byte, bool) {
	switch cs {
	case "ISO-8859-1":
		if code > 0xFF {
			return 0, false
		}
		return byte(code), true
	case "ISO-8859-5":
		if code <= 0xA0 || code == 0xAD {
			return byte(code), true
		}
		switch {
		case code == 0x2116:
			return 0xF0, true
		case code == 0xA7:
			return 0xFD, true
		case code >= 0x0401 && code <= 0x045F:
			if code == 0x040D || code == 0x0450 || code == 0x045D {
				return 0, false
			}
			return byte(code - 0x360), true
		}
		return 0, false
	case "ISO-8859-15":
		if code < 0xA4 || (code > 0xBE && code <= 0xFF) {
			return byte(code), true
		}
		v, ok := htmlUniMap[cs][code]
		return v, ok
	case "Windows-1252":
		if code <= 0x7F || (code >= 0xA0 && code <= 0xFF) {
			return byte(code), true
		}
		v, ok := htmlUniMap[cs][code]
		return v, ok
	case "MacRoman":
		if code == 0x7F {
			return 0, false
		}
		if code <= 0x7F {
			return byte(code), true
		}
		v, ok := htmlUniMap[cs][code]
		return v, ok
	case "CP1251", "KOI8-R", "CP866":
		if code <= 0x7F {
			return byte(code), true
		}
		v, ok := htmlUniMap[cs][code]
		return v, ok
	}
	return 0, false
}

// --- unicode_cp_is_allowed / numeric_entity_is_allowed ---

func htmlUnicodeAllowed(cp rune, doctype int64) bool {
	switch doctype {
	case 0: // HTML 4.01
		return (cp >= 0x20 && cp <= 0x7E) ||
			cp == 0x0A || cp == 0x09 || cp == 0x0D ||
			(cp >= 0xA0 && cp <= 0xD7FF) ||
			(cp >= 0xE000 && cp <= 0x10FFFF)
	case 48: // HTML 5
		return (cp >= 0x20 && cp <= 0x7E) ||
			(cp >= 0x09 && cp <= 0x0D && cp != 0x0B) ||
			(cp >= 0xA0 && cp <= 0xD7FF) ||
			(cp >= 0xE000 && cp <= 0x10FFFF &&
				(cp&0xFFFF) < 0xFFFE &&
				(cp < 0xFDD0 || cp > 0xFDEF))
	case 32, 16: // XHTML, XML1
		return (cp >= 0x20 && cp <= 0xD7FF) ||
			cp == 0x0A || cp == 0x09 || cp == 0x0D ||
			(cp >= 0xE000 && cp <= 0x10FFFF && cp != 0xFFFE && cp != 0xFFFF)
	}
	return true
}

func htmlNumericAllowed(cp rune, doctype int64) bool {
	switch doctype {
	case 0:
		return cp <= 0x10FFFF
	case 48:
		return (cp >= 0x20 && cp <= 0x7E) ||
			(cp >= 0x09 && cp <= 0x0C && cp != 0x0B) ||
			(cp >= 0xA0 && cp <= 0x10FFFF &&
				(cp&0xFFFF) < 0xFFFE &&
				(cp < 0xFDD0 || cp > 0xFDEF))
	case 32, 16:
		return htmlUnicodeAllowed(cp, doctype)
	}
	return true
}

// --- многобайтовые последовательности (get_next_char) ---

// htmlNextChar разбирает один символ в строке s начиная с i.
// Возвращает значение this_char и количество потреблённых байт.
func htmlNextChar(s string, i int, cs string) (rune, int, bool) {
	switch cs {
	case "UTF-8":
		return decodeUTF8Char(s, i)
	case "BIG5":
		c := s[i]
		if c >= 0x81 && c <= 0xFE {
			if i+1 >= len(s) {
				return 0, 1, false
			}
			n := s[i+1]
			if (n >= 0x40 && n <= 0x7E) || (n >= 0xA1 && n <= 0xFE) {
				return rune(c)<<8 | rune(n), 2, true
			}
			return 0, 1, false
		}
		return rune(c), 1, true
	case "BIG5-HKSCS":
		c := s[i]
		if c >= 0x81 && c <= 0xFE {
			if i+1 >= len(s) {
				return 0, 1, false
			}
			n := s[i+1]
			if (n >= 0x40 && n <= 0x7E) || (n >= 0xA1 && n <= 0xFE) {
				return rune(c)<<8 | rune(n), 2, true
			}
			if n != 0x80 && n != 0xFF {
				return 0, 1, false
			}
			return 0, 2, false
		}
		return rune(c), 1, true
	case "GB2312":
		c := s[i]
		if c >= 0xA1 && c <= 0xFE {
			if i+1 >= len(s) {
				return 0, 1, false
			}
			n := s[i+1]
			if n >= 0xA1 && n <= 0xFE {
				return rune(c)<<8 | rune(n), 2, true
			}
			if n != 0x8E && n != 0x8F && n != 0xA0 && n != 0xFF {
				return 0, 1, false
			}
			return 0, 2, false
		}
		if c != 0x8E && c != 0x8F && c != 0xA0 && c != 0xFF {
			return rune(c), 1, true
		}
		return 0, 1, false
	case "SJIS":
		c := s[i]
		if (c >= 0x81 && c <= 0x9F) || (c >= 0xE0 && c <= 0xFC) {
			if i+1 >= len(s) {
				return 0, 1, false
			}
			n := s[i+1]
			if n >= 0x40 && n != 0x7F && n < 0xFD {
				return rune(c)<<8 | rune(n), 2, true
			}
			if c != 0x80 && c != 0xA0 && c < 0xFD {
				return 0, 1, false
			}
			return 0, 2, false
		}
		if c < 0x80 || (c >= 0xA1 && c <= 0xDF) {
			return rune(c), 1, true
		}
		return 0, 1, false
	case "EUC-JP":
		c := s[i]
		if c >= 0xA1 && c <= 0xFE {
			if i+1 >= len(s) {
				return 0, 1, false
			}
			n := s[i+1]
			if n >= 0xA1 && n <= 0xFE {
				return rune(c)<<8 | rune(n), 2, true
			}
			if n != 0xA0 && n != 0xFF {
				return 0, 1, false
			}
			return 0, 2, false
		}
		if c == 0x8E {
			if i+1 >= len(s) {
				return 0, 1, false
			}
			n := s[i+1]
			if n >= 0xA1 && n <= 0xDF {
				return rune(c)<<8 | rune(n), 2, true
			}
			if n != 0xA0 && n != 0xFF {
				return 0, 1, false
			}
			return 0, 2, false
		}
		if c == 0x8F {
			if i+2 >= len(s) || !(s[i+1] >= 0xA1 && s[i+1] <= 0xFE) || !(s[i+2] >= 0xA1 && s[i+2] <= 0xFE) {
				if i+1 >= len(s) || (s[i+1] != 0xA0 && s[i+1] != 0xFF) {
					return 0, 1, false
				}
				if i+2 >= len(s) || (s[i+2] != 0xA0 && s[i+2] != 0xFF) {
					return 0, 2, false
				}
				return 0, 3, false
			}
			return rune(c)<<16 | rune(s[i+1])<<8 | rune(s[i+2]), 3, true
		}
		if c != 0xA0 && c != 0xFF {
			return rune(c), 1, true
		}
		return 0, 1, false
	}
	return rune(s[i]), 1, true
}

// --- разбор числовых сущностей ---

// htmlParseNumericEntity разбирает "&#..." начиная с i (s[i]=='#').
// Возвращает кодпойнт и индекс ';'.
func htmlParseNumericEntity(s string, i int) (code int64, semi int, ok bool) {
	j := i + 1
	hex := false
	if j < len(s) && (s[j] == 'x' || s[j] == 'X') {
		hex = true
		j++
	}
	start := j
	for j < len(s) {
		c := s[j]
		if hex {
			if !isHexDigit(c) {
				break
			}
		} else if c < '0' || c > '9' {
			break
		}
		j++
	}
	if j == start || j >= len(s) || s[j] != ';' {
		return 0, i, false
	}
	base := 10
	if hex {
		base = 16
	}
	v, err := strconv.ParseInt(s[start:j], base, 64)
	if err != nil || v > 0x10FFFF {
		return 0, i, false
	}
	return v, j, true
}

// --- именованные сущности для проверки double_encode ---

func htmlNamedEntityValid(name string, doctype int64) bool {
	switch doctype {
	case 48:
		if _, ok := html5EntityNameToRune[name]; ok {
			return true
		}
		_, ok := html4EntityNameToRune[name]
		return ok
	case 32: // XHTML
		if name == "apos" {
			return true
		}
		_, ok := html4EntityNameToRune[name]
		return ok
	case 16: // XML1
		switch name {
		case "amp", "lt", "gt", "quot", "apos", "#039":
			return true
		}
		return false
	default: // HTML 4.01
		_, ok := html4EntityNameToRune[name]
		return ok
	}
}

// --- кодирование ---

var htmlBasicBE = map[rune]string{34: "&quot;", 38: "&amp;", 60: "&lt;", 62: "&gt;"}

// htmlEntityForRune возвращает именованную сущность для кодпойнта.
func htmlEntityForRune(r rune, doctype int64, all bool) (string, bool) {
	if r == '\'' {
		if all {
			if doctype == 48 {
				return "&apos;", true
			}
			return "&#039;", true
		}
		if doctype == 0 {
			return "&#039;", true
		}
		return "&apos;", true
	}
	if all {
		if doctype == 48 {
			// ENT_HTML5: только HTML5-таблица. У U+2329/U+232A (HTML4
			// &lang;/&rang;) в HTML5 сущностей нет — откат на HTML4-таблицу
			// давал их ошибочную замену.
			if e, ok := html5EntityByRune[r]; ok {
				return e, true
			}
			return "", false
		}
		if e, ok := html4EntityByRune[r]; ok {
			return e, true
		}
		return "", false
	}
	if e, ok := htmlBasicBE[r]; ok {
		return e, true
	}
	return "", false
}

// html5MultiEntity — многокодовая сущность HTML5 (напр. U+003C U+20D2 ->
// &nvlt;). Таблица-источник — html5TableUTF8 (инверсия справочной таблицы
// PHP 8.3), поэтому имена совпадают с каноническими именами php-src.
type html5MultiEntity struct {
	seq string
	ent string
}

// html5MultiEntityByFirst — многокодовые сущности по первой руне.
var html5MultiEntityByFirst = func() map[rune][]html5MultiEntity {
	m := map[rune][]html5MultiEntity{}
	for seq, ent := range html5TableUTF8 {
		rs := []rune(seq)
		if len(rs) < 2 {
			continue
		}
		m[rs[0]] = append(m[rs[0]], html5MultiEntity{seq: seq, ent: ent})
	}
	return m
}()

// html5MultiEntityAt ищет самую длинную многокодовую сущность в позиции start
// (вход в UTF-8).
func html5MultiEntityAt(s string, start int) (string, int, bool) {
	first, adv, ok := htmlNextChar(s, start, "UTF-8")
	if !ok || adv <= 0 {
		return "", 0, false
	}
	bestEnt, bestLen := "", 0
	for _, e := range html5MultiEntityByFirst[first] {
		if len(e.seq) > bestLen && strings.HasPrefix(s[start:], e.seq) {
			bestEnt, bestLen = e.ent, len(e.seq)
		}
	}
	if bestLen > 0 {
		return bestEnt, bestLen, true
	}
	return "", 0, false
}

// htmlEncodeCore реализует php_escape_html_entities_ex.
func htmlEncodeCore(s string, flags int64, doubleEncode, allArg bool, enc string) string {
	cs := enc
	doctype := flags & 48
	quote := flags & 3
	all := allArg && !htmlCharsetPartial(cs) && doctype != 16

	replacement := "&#xFFFD;"
	if cs == "UTF-8" {
		replacement = "\uFFFD"
	}

	var toUni *[256]rune
	if all && !htmlCharsetUnicodeCompat(cs) {
		toUni = htmlEncToUni[cs]
	}

	var b strings.Builder
	i := 0
	for i < len(s) {
		start := i
		this, adv, ok := htmlNextChar(s, i, cs)
		if adv <= 0 {
			adv = 1
		}
		i = start + adv
		if !ok {
			if flags&4 != 0 { // ENT_IGNORE
				continue
			}
			if flags&8 != 0 { // ENT_SUBSTITUTE
				b.WriteString(replacement)
				continue
			}
			return ""
		}
		mbseq := s[start : start+adv]

		if this != '&' {
			if (this == '\'' && quote&1 == 0) || (this == '"' && quote&2 == 0) {
				b.WriteString(mbseq)
				continue
			}
			cp := this
			known := true
			if all {
				if toUni != nil {
					cp = toUni[byte(this)]
					if cp == 0xFFFF {
						b.WriteString(mbseq)
						continue
					}
				}
			}
			// HTML5: многокодовые сущности (&nvlt; и т.п.) имеют приоритет
			// над одиночными (php_escape_html_entities_ex).
			if all && doctype == 48 && cs == "UTF-8" {
				if ent, n, ok := html5MultiEntityAt(s, start); ok {
					b.WriteString(ent)
					i = start + n
					continue
				}
			}
			if e, ok := htmlEntityForRune(cp, doctype, all); ok {
				b.WriteString(e)
				continue
			}
			if flags&128 != 0 { // ENT_DISALLOWED
				if htmlCharsetUnicodeCompat(cs) || toUni != nil {
					if cp > 0x10FFFF || !htmlUnicodeAllowed(cp, doctype) {
						b.WriteString(replacement)
						continue
					}
				} else if known && this <= 0x7D {
					if !htmlUnicodeAllowed(this, doctype) {
						b.WriteString(replacement)
						continue
					}
				}
			}
			b.WriteString(mbseq)
			continue
		}

		// this == '&'
		if doubleEncode {
			b.WriteString("&amp;")
			continue
		}
		next := start + 1
		if next < len(s) && s[next] == '#' {
			code, semi, ok := htmlParseNumericEntity(s, next)
			if !ok {
				b.WriteString("&amp;")
				continue
			}
			if flags&128 != 0 && !htmlNumericAllowed(rune(code), doctype) {
				b.WriteString("&amp;")
				continue
			}
			b.WriteString(s[start : semi+1])
			i = semi + 1
			continue
		}
		j := next
		for j < len(s) && isAlnumByte(s[j]) {
			j++
		}
		if j >= len(s) || s[j] != ';' || j == next {
			b.WriteString("&amp;")
			continue
		}
		name := s[next:j]
		if !htmlNamedEntityValid(name, doctype) {
			b.WriteString("&amp;")
			continue
		}
		b.WriteString(s[start : j+1])
		i = j + 1
	}
	return b.String()
}

func isAlnumByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// --- декодирование (php_unescape_html_entities) ---

// htmlDecodeEntity решает, декодируется ли числовая сущность (all=true путь).
func htmlDecodeNumeric(s string, i int, flags int64, doctype int64, cs string) (out []byte, next int, ok bool) {
	// s[i] == '#'
	code, semi, valid := htmlParseNumericEntity(s, i)
	if !valid {
		return nil, i, false
	}
	if !htmlUnicodeAllowed(rune(code), doctype) || (doctype == 48 && code == 0x0D) {
		return nil, i, false
	}
	if (code == 39 && flags&1 == 0) || (code == 34 && flags&2 == 0) {
		return nil, i, false
	}
	if cs != "UTF-8" {
		b, r := htmlMapFromUnicode(rune(code), cs)
		if !r {
			return nil, i, false
		}
		return []byte{b}, semi + 1, true
	}
	return encodeRuneUTF8(rune(code)), semi + 1, true
}

func encodeRuneUTF8(r rune) []byte {
	if r < 0x80 {
		return []byte{byte(r)}
	}
	if r < 0x800 {
		return []byte{byte(0xC0 | r>>6), byte(0x80 | r&0x3F)}
	}
	if r < 0x10000 {
		return []byte{byte(0xE0 | r>>12), byte(0x80 | (r>>6)&0x3F), byte(0x80 | r&0x3F)}
	}
	return []byte{byte(0xF0 | r>>18), byte(0x80 | (r>>12)&0x3F), byte(0x80 | (r>>6)&0x3F), byte(0x80 | r&0x3F)}
}

// htmlDecodeAll реализует html_entity_decode (all=true).
func htmlDecodeAll(s string, flags int64, cs string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	doctype := flags & 48
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '&' || i+3 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		next := i + 2
		var dec []byte
		ok := false
		if s[i+1] == '#' {
			dec, next, ok = htmlDecodeNumeric(s, i+1, flags, doctype, cs)
		} else {
			j := i + 1
			for j < len(s) && isAlnumByte(s[j]) {
				j++
			}
			if j < len(s) && s[j] == ';' && j > i+1 {
				name := s[i+1 : j]
				cp, cp2, r := htmlResolveNamed(name, doctype)
				if r && cp2 == 0 {
					if (cp == '\'' && flags&1 == 0) || (cp == '"' && flags&2 == 0) {
						r = false
					}
				}
				if r && cp2 == 0 && cs != "UTF-8" {
					if _, rr := htmlMapFromUnicode(cp, cs); !rr {
						r = false
					}
				}
				if r {
					var buf []byte
					if cs == "UTF-8" {
						buf = append(buf, encodeRuneUTF8(cp)...)
						if cp2 != 0 {
							buf = append(buf, encodeRuneUTF8(cp2)...)
						}
					} else {
						bb, _ := htmlMapFromUnicode(cp, cs)
						buf = append(buf, bb)
						if cp2 != 0 {
							b2, _ := htmlMapFromUnicode(cp2, cs)
							buf = append(buf, b2)
						}
					}
					dec = buf
					next = j + 1
					ok = true
				}
			}
		}
		if ok {
			b.Write(dec)
			i = next
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func htmlResolveNamed(name string, doctype int64) (rune, rune, bool) {
	switch doctype {
	case 48:
		if v, ok := html5EntityNameToRune[name]; ok {
			rs := []rune(v)
			if len(rs) == 1 {
				return rs[0], 0, true
			}
			if len(rs) >= 2 {
				return rs[0], rs[1], true
			}
		}
		if v, ok := html4EntityNameToRune[name]; ok {
			rs := []rune(v)
			if len(rs) >= 1 {
				return rs[0], 0, true
			}
		}
		return 0, 0, false
	case 32: // XHTML
		if v, ok := html4EntityNameToRune[name]; ok {
			rs := []rune(v)
			if len(rs) >= 1 {
				return rs[0], 0, true
			}
		}
		if name == "apos" || name == "#039" {
			return '\'', 0, true
		}
		return 0, 0, false
	case 16: // XML1 / basic
		switch name {
		case "amp":
			return '&', 0, true
		case "lt":
			return '<', 0, true
		case "gt":
			return '>', 0, true
		case "quot":
			return '"', 0, true
		case "apos", "#039":
			return '\'', 0, true
		}
		return 0, 0, false
	default: // HTML 4.01
		if v, ok := html4EntityNameToRune[name]; ok {
			rs := []rune(v)
			if len(rs) >= 1 {
				return rs[0], 0, true
			}
		}
		return 0, 0, false
	}
}

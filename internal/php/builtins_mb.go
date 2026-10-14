package php

import (
	"fmt"
	"phpvm/internal/vm"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Файл содержит mb_*/iconv и таблицы кодировок.

// --- iconv ---

// iconvEncoding разбирает имя кодировки, отбрасывая суффиксы //IGNORE, //TRANSLIT.
func iconvEncoding(enc string) (string, bool) {
	base := enc
	if i := strings.Index(base, "//"); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return "", false
	}
	if c, ok := mbCanon(base); ok {
		return c, true
	}
	return "", false
}

// iconvBytes преобразует байты из кодировки from в to (для convert.iconv).
func iconvBytes(from, to string, b []byte) []byte {
	f, ok := iconvEncoding(from)
	if !ok {
		return b
	}
	t, ok2 := iconvEncoding(to)
	if !ok2 {
		return b
	}
	return []byte(mbEncode(mbDecode(string(b), f), t))
}

func iconvFn(ctx *vm.Context) vm.Value {
	from, ok := iconvEncoding(arg(ctx, 0).ToString())
	if !ok {
		return vm.Bool(false)
	}
	to, ok := iconvEncoding(arg(ctx, 1).ToString())
	if !ok {
		return vm.Bool(false)
	}
	s := arg(ctx, 2).ToString()
	return vm.Str(mbEncode(mbDecode(s, from), to))
}

// obIconvHandlerFn — ob_iconv_handler(): обработчик буфера вывода,
// перекодирующий данные между iconv.internal_encoding и
// iconv.output_encoding. VM моделирует кодировки как UTF-8, поэтому данные
// возвращаются без изменений (функция должна существовать: ob_start
// проверяет её наличие).
func obIconvHandlerFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		return vm.Str("")
	}
	return vm.Str(ctx.Args()[0].ToString())
}

func iconvStrlenFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 1, "iconv_strlen")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.Int(int64(len(mbDecode(arg0(ctx).ToString(), enc))))
}

func iconvSubstrFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 3, "iconv_substr")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	rs := mbDecode(arg0(ctx).ToString(), enc)
	start := int(arg(ctx, 1).ToInt())
	if start < 0 {
		start = len(rs) + start
		if start < 0 {
			start = 0
		}
	}
	if start > len(rs) {
		return vm.Str("")
	}
	a := ctx.Args()
	if len(a) >= 3 && a[2].Type() != vm.NullType {
		length := int(arg(ctx, 2).ToInt())
		if length < 0 {
			end := len(rs) + length
			if end < start {
				return vm.Str("")
			}
			return vm.Str(mbEncode(rs[start:end], enc))
		}
		if start+length > len(rs) {
			return vm.Str(mbEncode(rs[start:], enc))
		}
		return vm.Str(mbEncode(rs[start:start+length], enc))
	}
	return vm.Str(mbEncode(rs[start:], enc))
}

func iconvStrposFn(ctx *vm.Context, last bool) vm.Value {
	enc := mbArgEnc(ctx, 3, "iconv_strpos")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	h := mbDecode(arg(ctx, 0).ToString(), enc)
	n := mbDecode(arg(ctx, 1).ToString(), enc)
	off := 0
	a := ctx.Args()
	if len(a) >= 3 && a[2].Type() != vm.NullType {
		off = int(arg(ctx, 2).ToInt())
	}
	if last {
		start, end := 0, len(h)
		if off > 0 {
			start = off
		}
		if off < 0 {
			end = len(h) + off
			if end < 0 {
				end = 0
			}
		}
		if start > end {
			return vm.Bool(false)
		}
		idx := lastIndexRunes(h[start:end], n)
		if idx < 0 {
			return vm.Bool(false)
		}
		return vm.Int(int64(idx + start))
	}
	start := off
	if start < 0 {
		start = len(h) + start
		if start < 0 {
			start = 0
		}
	}
	if start > len(h) {
		return vm.Bool(false)
	}
	idx := indexRunes(h[start:], n)
	if idx < 0 {
		return vm.Bool(false)
	}
	return vm.Int(int64(idx + start))
}

// ============================================================================
// mbstring (mb_*). Строки PHP — байтовые; внутри функции при необходимости
// декодируют байты в руны согласно кодировке (UTF-8 по умолчанию) и кодируют
// результат обратно. Поддерживаются UTF-8/ISO-8859-1/ASCII/CP1251/UTF-16/UTF-32,
// для прочих известных кодировок — latin1-заглушка.
// ============================================================================

// mbInternalEncoding — текущая внутренняя кодировка mbstring.
var mbInternalEncoding = "UTF-8"

// mbEncodingList повторяет mb_list_encodings() живого PHP 8.3.
var mbEncodingList = []string{
	"BASE64", "UUENCODE", "HTML-ENTITIES", "Quoted-Printable", "7bit", "8bit",
	"UCS-4", "UCS-4BE", "UCS-4LE", "UCS-2", "UCS-2BE", "UCS-2LE",
	"UTF-32", "UTF-32BE", "UTF-32LE", "UTF-16", "UTF-16BE", "UTF-16LE",
	"UTF-8", "UTF-7", "UTF7-IMAP", "ASCII", "EUC-JP", "SJIS", "eucJP-win",
	"EUC-JP-2004", "SJIS-Mobile#DOCOMO", "SJIS-Mobile#KDDI",
	"SJIS-Mobile#SOFTBANK", "SJIS-mac", "SJIS-2004", "UTF-8-Mobile#DOCOMO",
	"UTF-8-Mobile#KDDI-A", "UTF-8-Mobile#KDDI-B", "UTF-8-Mobile#SOFTBANK",
	"CP932", "SJIS-win", "CP51932", "JIS", "ISO-2022-JP", "ISO-2022-JP-MS",
	"GB18030", "Windows-1252", "Windows-1254", "ISO-8859-1", "ISO-8859-2",
	"ISO-8859-3", "ISO-8859-4", "ISO-8859-5", "ISO-8859-6", "ISO-8859-7",
	"ISO-8859-8", "ISO-8859-9", "ISO-8859-10", "ISO-8859-13", "ISO-8859-14",
	"ISO-8859-15", "ISO-8859-16", "EUC-CN", "CP936", "HZ", "EUC-TW", "BIG-5",
	"CP950", "EUC-KR", "UHC", "ISO-2022-KR", "Windows-1251", "CP866", "KOI8-R",
	"KOI8-U", "ArmSCII-8", "CP850", "ISO-2022-JP-2004",
	"ISO-2022-JP-MOBILE#KDDI", "CP50220", "CP50221", "CP50222",
}

// mbNormEnc нормализует имя кодировки: верхний регистр без '-', '_' и пробелов.
func mbNormEnc(enc string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(enc) {
		if r == '-' || r == '_' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mbCanon возвращает каноническое имя кодировки и true, если имя известно.
func mbCanon(enc string) (string, bool) {
	n := mbNormEnc(enc)
	switch n {
	case "UTF8", "UTF8MOBILEDOCOMO", "UTF8MOBILEKDDIA", "UTF8MOBILEKDDIB",
		"UTF8MOBILESOFTBANK":
		return "UTF-8", true
	case "UTF16":
		return "UTF-16", true
	case "UTF16BE":
		return "UTF-16BE", true
	case "UTF16LE":
		return "UTF-16LE", true
	case "UTF32":
		return "UTF-32", true
	case "UTF32BE":
		return "UTF-32BE", true
	case "UTF32LE":
		return "UTF-32LE", true
	case "UCS2", "ISO10646UCS2", "UNICODE":
		return "UTF-16", true
	case "UCS2BE", "BYTE2BE":
		return "UTF-16BE", true
	case "UCS2LE", "BYTE2LE":
		return "UTF-16LE", true
	case "UCS4", "ISO10646UCS4":
		return "UTF-32", true
	case "UCS4BE", "BYTE4BE":
		return "UTF-32BE", true
	case "UCS4LE", "BYTE4LE":
		return "UTF-32LE", true
	case "ASCII", "USASCII", "ANSIX3.41968", "ISOIR6", "ANSIX3.41986",
		"ISO646.IRV:1991", "ISO646US", "US", "IBM367", "CP367", "CSASCII":
		return "ASCII", true
	case "ISO88591", "ISO8859", "LATIN1":
		return "ISO-8859-1", true
	case "CP1251", "WINDOWS1251":
		return "CP1251", true
	case "CP1252", "WINDOWS1252":
		return "Windows-1252", true
	case "SHIFTJIS", "SJIS":
		return "SJIS", true
	case "ISO885915", "LATIN9":
		return "ISO-8859-15", true
	case "BINARY":
		return "8bit", true
	}
	for _, e := range mbEncodingList {
		if mbNormEnc(e) == n {
			return e, true
		}
	}
	return "", false
}

// mbArgEnc читает необязательный аргумент-кодировку (i), иначе — внутреннюю.
func mbArgEnc(ctx *vm.Context, i int, fname string) string {
	a := ctx.Args()
	if i < 0 || i >= len(a) || a[i].Type() == vm.NullType {
		return mbInternalEncoding
	}
	s := a[i].ToString()
	if s == "" {
		return mbInternalEncoding
	}
	c, ok := mbCanon(s)
	if !ok {
		ctx.Throw(vm.NewThrowable("ValueError", fmt.Sprintf(
			"%s(): Argument #%d ($encoding) must be a valid encoding, \"%s\" given", fname, i+1, s)))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	return c
}

// --- таблицы CP1251 (Windows-1251) ---

var mbCP1251High = [128]rune{
	0x0402, 0x0403, 0x201A, 0x0453, 0x201E, 0x2026, 0x2020, 0x2021,
	0x20AC, 0x2030, 0x0409, 0x2039, 0x040A, 0x040C, 0x040B, 0x040F,
	0x0452, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x0098, 0x2122, 0x0459, 0x203A, 0x045A, 0x045C, 0x045B, 0x045F,
	0x00A0, 0x040E, 0x045E, 0x0408, 0x00A4, 0x0490, 0x00A6, 0x00A7,
	0x0401, 0x00A9, 0x0404, 0x00AB, 0x00AC, 0x00AD, 0x00AE, 0x0407,
	0x00B0, 0x00B1, 0x0406, 0x0456, 0x0491, 0x00B5, 0x00B6, 0x00B7,
	0x0451, 0x2116, 0x0454, 0x00BB, 0x0458, 0x0405, 0x0455, 0x0457,
	0x0410, 0x0411, 0x0412, 0x0413, 0x0414, 0x0415, 0x0416, 0x0417,
	0x0418, 0x0419, 0x041A, 0x041B, 0x041C, 0x041D, 0x041E, 0x041F,
	0x0420, 0x0421, 0x0422, 0x0423, 0x0424, 0x0425, 0x0426, 0x0427,
	0x0428, 0x0429, 0x042A, 0x042B, 0x042C, 0x042D, 0x042E, 0x042F,
	0x0430, 0x0431, 0x0432, 0x0433, 0x0434, 0x0435, 0x0436, 0x0437,
	0x0438, 0x0439, 0x043A, 0x043B, 0x043C, 0x043D, 0x043E, 0x043F,
	0x0440, 0x0441, 0x0442, 0x0443, 0x0444, 0x0445, 0x0446, 0x0447,
	0x0448, 0x0449, 0x044A, 0x044B, 0x044C, 0x044D, 0x044E, 0x044F,
}

var mbCP1251From = func() map[rune]byte {
	m := make(map[rune]byte, 256)
	for i := 0; i < 128; i++ {
		m[rune(i)] = byte(i)
	}
	for i, r := range mbCP1251High {
		m[r] = byte(0x80 + i)
	}
	return m
}()

func mbLatin1Runes(s string) []rune {
	r := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		r[i] = rune(s[i])
	}
	return r
}

func mbCP1251Decode(s string) []rune {
	r := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x80 {
			r[i] = rune(b)
		} else {
			r[i] = mbCP1251High[b-0x80]
		}
	}
	return r
}

func mbCP1251Encode(rs []rune) string {
	b := make([]byte, 0, len(rs))
	for _, r := range rs {
		if v, ok := mbCP1251From[r]; ok {
			b = append(b, v)
		} else {
			b = append(b, '?')
		}
	}
	return string(b)
}

func mbU16(u uint16, le bool) []byte {
	if le {
		return []byte{byte(u), byte(u >> 8)}
	}
	return []byte{byte(u >> 8), byte(u)}
}

func mbDecodeUTF16(s, order string) []rune {
	le := order == "LE"
	b := []byte(s)
	if order == "" {
		if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
			le, b = true, b[2:]
		} else if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
			b = b[2:]
		}
	}
	out := make([]rune, 0, len(b)/2+1)
	for i := 0; i+1 < len(b); i += 2 {
		var u uint16
		if le {
			u = uint16(b[i]) | uint16(b[i+1])<<8
		} else {
			u = uint16(b[i])<<8 | uint16(b[i+1])
		}
		if u >= 0xD800 && u <= 0xDBFF && i+3 < len(b) {
			var u2 uint16
			if le {
				u2 = uint16(b[i+2]) | uint16(b[i+3])<<8
			} else {
				u2 = uint16(b[i+2])<<8 | uint16(b[i+3])
			}
			if u2 >= 0xDC00 && u2 <= 0xDFFF {
				out = append(out, rune(0x10000+(uint32(u-0xD800)<<10)+uint32(u2-0xDC00)))
				i += 2
				continue
			}
		}
		out = append(out, rune(u))
	}
	return out
}

func mbEncodeUTF16(rs []rune, order string) string {
	le := order == "LE"
	b := make([]byte, 0, len(rs)*2)
	for _, r := range rs {
		if r > 0xFFFF {
			r -= 0x10000
			b = append(b, mbU16(uint16(0xD800+(r>>10)), le)...)
			b = append(b, mbU16(uint16(0xDC00+(r&0x3FF)), le)...)
		} else {
			b = append(b, mbU16(uint16(r), le)...)
		}
	}
	return string(b)
}

func mbDecodeUTF32(s, order string) []rune {
	b := []byte(s)
	if order == "" {
		if len(b) >= 4 && b[0] == 0x00 && b[1] == 0x00 && b[2] == 0xFE && b[3] == 0xFF {
			b = b[4:]
		} else if len(b) >= 4 && b[0] == 0xFF && b[1] == 0xFE && b[2] == 0x00 && b[3] == 0x00 {
			order, b = "LE", b[4:]
		}
	}
	le := order == "LE"
	out := make([]rune, 0, len(b)/4)
	for i := 0; i+3 < len(b); i += 4 {
		var u uint32
		if le {
			u = uint32(b[i]) | uint32(b[i+1])<<8 | uint32(b[i+2])<<16 | uint32(b[i+3])<<24
		} else {
			u = uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8 | uint32(b[i+3])
		}
		out = append(out, rune(u))
	}
	return out
}

func mbEncodeUTF32(rs []rune, order string) string {
	le := order == "LE"
	b := make([]byte, 0, len(rs)*4)
	for _, r := range rs {
		u := uint32(r)
		if le {
			b = append(b, byte(u), byte(u>>8), byte(u>>16), byte(u>>24))
		} else {
			b = append(b, byte(u>>24), byte(u>>16), byte(u>>8), byte(u))
		}
	}
	return string(b)
}

func mbDecode(s, enc string) []rune {
	switch enc {
	case "UTF-8":
		return []rune(s)
	case "ISO-8859-1", "ASCII":
		return mbLatin1Runes(s)
	case "CP1251":
		return mbCP1251Decode(s)
	case "Windows-1252":
		return mbCP1252Decode(s)
	case "ISO-8859-15":
		return mbLatin9Decode(s)
	case "UTF-16":
		return mbDecodeUTF16(s, "")
	case "UTF-16BE":
		return mbDecodeUTF16(s, "BE")
	case "UTF-16LE":
		return mbDecodeUTF16(s, "LE")
	case "UTF-32":
		return mbDecodeUTF32(s, "")
	case "UTF-32BE":
		return mbDecodeUTF32(s, "BE")
	case "UTF-32LE":
		return mbDecodeUTF32(s, "LE")
	}
	return mbLatin1Runes(s)
}

func mbEncode(rs []rune, enc string) string {
	switch enc {
	case "UTF-8":
		return string(rs)
	case "ISO-8859-1":
		b := make([]byte, 0, len(rs))
		for _, r := range rs {
			if r <= 0xFF {
				b = append(b, byte(r))
			} else {
				b = append(b, '?')
			}
		}
		return string(b)
	case "ASCII":
		b := make([]byte, 0, len(rs))
		for _, r := range rs {
			if r < 0x80 {
				b = append(b, byte(r))
			} else {
				b = append(b, '?')
			}
		}
		return string(b)
	case "CP1251":
		return mbCP1251Encode(rs)
	case "Windows-1252":
		return mbCP1252Encode(rs)
	case "ISO-8859-15":
		return mbLatin9Encode(rs)
	case "UTF-16":
		return mbEncodeUTF16(rs, "BE")
	case "UTF-16BE":
		return mbEncodeUTF16(rs, "BE")
	case "UTF-16LE":
		return mbEncodeUTF16(rs, "LE")
	case "UTF-32":
		return mbEncodeUTF32(rs, "BE")
	case "UTF-32BE":
		return mbEncodeUTF32(rs, "BE")
	case "UTF-32LE":
		return mbEncodeUTF32(rs, "LE")
	}
	b := make([]byte, 0, len(rs))
	for _, r := range rs {
		if r <= 0xFF {
			b = append(b, byte(r))
		} else {
			b = append(b, '?')
		}
	}
	return string(b)
}

func mbCheckEncoding(s, enc string) bool {
	switch enc {
	case "UTF-8":
		return utf8.ValidString(s)
	case "ASCII":
		for i := 0; i < len(s); i++ {
			if s[i] >= 0x80 {
				return false
			}
		}
		return true
	case "UTF-16", "UTF-16BE", "UTF-16LE":
		return len(s)%2 == 0
	case "UTF-32", "UTF-32BE", "UTF-32LE":
		return len(s)%4 == 0
	}
	return true
}

func mbConvertString(s, from, to string) string {
	if from == to {
		return s
	}
	return mbEncode(mbDecode(s, from), to)
}

func indexRunes(h, n []rune) int {
	if len(n) == 0 {
		return 0
	}
	for i := 0; i+len(n) <= len(h); i++ {
		ok := true
		for j := range n {
			if h[i+j] != n[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func lastIndexRunes(h, n []rune) int {
	if len(n) == 0 {
		return len(h)
	}
	for i := len(h) - len(n); i >= 0; i-- {
		ok := true
		for j := range n {
			if h[i+j] != n[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// --- зарегистрированные функции ---

func mbInternalEncodingFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		return vm.Str(mbInternalEncoding)
	}
	s := arg0(ctx).ToString()
	c, ok := mbCanon(s)
	if !ok {
		ctx.Throw(vm.NewThrowable("ValueError", fmt.Sprintf(
			"mb_internal_encoding(): Argument #1 ($encoding) must be a valid encoding, \"%s\" given", s)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	mbInternalEncoding = c
	return vm.Bool(true)
}

func mbListEncodingsFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, e := range mbEncodingList {
		out.Append(vm.MakeString(e))
	}
	return vm.MakeArray(out)
}

func mbEncodingAliasesFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	c, ok := mbCanon(s)
	if !ok {
		ctx.Throw(vm.NewThrowable("ValueError", fmt.Sprintf(
			"mb_encoding_aliases(): Argument #1 ($encoding) must be a valid encoding, \"%s\" given", s)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	var aliases []string
	switch c {
	case "UTF-8":
		aliases = []string{"utf8"}
	case "UTF-16":
		aliases = []string{"utf16"}
	case "UTF-32":
		aliases = []string{"utf32"}
	case "ASCII":
		aliases = []string{"ANSI_X3.4-1968", "iso-ir-6", "ANSI_X3.4-1986",
			"ISO_646.irv:1991", "US-ASCII", "ISO646-US", "us", "IBM367",
			"IBM-367", "cp367", "csASCII"}
	case "ISO-8859-1":
		aliases = []string{"ISO8859-1", "latin1"}
	case "CP1251":
		aliases = []string{"CP1251", "CP-1251", "WINDOWS-1251"}
	case "UCS-2":
		aliases = []string{"ISO-10646-UCS-2", "UCS2", "UNICODE"}
	case "UCS-4":
		aliases = []string{"ISO-10646-UCS-4", "UCS4"}
	case "UTF-7":
		aliases = []string{"utf7"}
	case "8bit":
		aliases = []string{"binary"}
	}
	out := vm.NewArray()
	for _, a := range aliases {
		out.Append(vm.MakeString(a))
	}
	return vm.MakeArray(out)
}

func mbCheckEncodingFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 1, "mb_check_encoding")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	v := arg0(ctx)
	if v.Type() == vm.ArrayType {
		for _, x := range v.AsArray().All() {
			if !mbCheckEncoding(x.ToString(), enc) {
				return vm.Bool(false)
			}
		}
		return vm.Bool(true)
	}
	return vm.Bool(mbCheckEncoding(v.ToString(), enc))
}

func mbDetectEncodingFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	var order []string
	a := ctx.Args()
	if len(a) >= 2 && a[1].Type() != vm.NullType {
		if a[1].Type() == vm.ArrayType {
			for _, v := range a[1].AsArray().All() {
				order = append(order, v.ToString())
			}
		} else {
			for _, p := range strings.Split(a[1].ToString(), ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					order = append(order, p)
				}
			}
		}
	} else {
		order = []string{"ASCII", "UTF-8"}
	}
	for _, e := range order {
		c, ok := mbCanon(e)
		if !ok {
			continue
		}
		if mbCheckEncoding(s, c) {
			return vm.Str(e)
		}
	}
	return vm.Bool(false)
}

func mbConvertEncVal(v vm.Slot, from, to string) vm.Value {
	if v.Type() == vm.ArrayType {
		out := vm.NewArray()
		for k, val := range v.AsArray().All() {
			var vs vm.Slot
			vm.SlotOf(&vs, mbConvertEncVal(val, from, to))
			out.Set(k, vs)
		}
		return vm.MakeArray(out)
	}
	return vm.Str(mbConvertString(v.ToString(), from, to))
}

func mbConvertEncodingFn(ctx *vm.Context) vm.Value {
	from := mbArgEnc(ctx, 2, "mb_convert_encoding")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	to := arg(ctx, 1).ToString()
	tc, ok := mbCanon(to)
	if !ok {
		ctx.Throw(vm.NewThrowable("ValueError", fmt.Sprintf(
			"mb_convert_encoding(): Argument #2 ($to_encoding) must be a valid encoding, \"%s\" given", to)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return mbConvertEncVal(arg0(ctx), from, tc)
}

func mbDeref(s vm.Slot) vm.Slot {
	for i := 0; i < 1000 && s.IsRef(); i++ {
		r := s.Ref()
		if r == nil {
			return vm.MakeNull()
		}
		s = r.Value()
	}
	return s
}

func mbConvertSlot(s vm.Slot, from, to string) vm.Slot {
	switch s.Type() {
	case vm.StringType:
		return vm.MakeString(mbConvertString(s.ToString(), from, to))
	case vm.ArrayType:
		arr := s.AsArray()
		type kv struct {
			k vm.ArrayKey
			v vm.Slot
		}
		var items []kv
		for k, v := range arr.All() {
			items = append(items, kv{k, v})
		}
		for _, it := range items {
			arr.Set(it.k, mbConvertSlot(it.v, from, to))
		}
		return s
	}
	return s
}

func mbConvertVariablesFn(ctx *vm.Context) vm.Value {
	to := arg(ctx, 0).ToString()
	from := arg(ctx, 1).ToString()
	tc, ok := mbCanon(to)
	if !ok {
		ctx.Throw(vm.NewThrowable("ValueError", fmt.Sprintf(
			"mb_convert_variables(): Argument #1 ($to_encoding) must be a valid encoding, \"%s\" given", to)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	fc, ok2 := mbCanon(from)
	if !ok2 {
		ctx.Throw(vm.NewThrowable("ValueError", fmt.Sprintf(
			"mb_convert_variables(): Argument #2 ($from_encoding) must be a valid encoding, \"%s\" given", from)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if tc != fc {
		a := ctx.Args()
		for i := 2; i < len(a); i++ {
			if !a[i].IsRef() || a[i].Ref() == nil {
				continue
			}
			cell := a[i].Ref()
			nv := mbConvertSlot(mbDeref(cell.Value()), fc, tc)
			cell.Set(nv)
		}
	}
	return vm.Str(from)
}

func mbStrlenFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 1, "mb_strlen")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.Int(int64(len(mbDecode(arg0(ctx).ToString(), enc))))
}

func mbSubstrFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 3, "mb_substr")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	rs := mbDecode(arg0(ctx).ToString(), enc)
	start := int(arg(ctx, 1).ToInt())
	if start < 0 {
		start = len(rs) + start
		if start < 0 {
			start = 0
		}
	}
	if start > len(rs) {
		return vm.Str("")
	}
	a := ctx.Args()
	if len(a) >= 3 && a[2].Type() != vm.NullType {
		length := int(arg(ctx, 2).ToInt())
		if length < 0 {
			end := len(rs) + length
			if end < start {
				return vm.Str("")
			}
			return vm.Str(mbEncode(rs[start:end], enc))
		}
		if start+length > len(rs) {
			return vm.Str(mbEncode(rs[start:], enc))
		}
		return vm.Str(mbEncode(rs[start:start+length], enc))
	}
	return vm.Str(mbEncode(rs[start:], enc))
}

func mbStrposFn(ctx *vm.Context, ci, last bool) vm.Value {
	name := "mb_strpos"
	switch {
	case ci && last:
		name = "mb_strripos"
	case ci:
		name = "mb_stripos"
	case last:
		name = "mb_strrpos"
	}
	enc := mbArgEnc(ctx, 3, name)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	h := mbDecode(arg(ctx, 0).ToString(), enc)
	n := mbDecode(arg(ctx, 1).ToString(), enc)
	if ci {
		h = []rune(strings.ToLower(string(h)))
		n = []rune(strings.ToLower(string(n)))
	}
	off := 0
	a := ctx.Args()
	if len(a) >= 3 && a[2].Type() != vm.NullType {
		off = int(arg(ctx, 2).ToInt())
	}
	var idx int
	if last {
		start, end := 0, len(h)
		if off > 0 {
			start = off
		}
		if off < 0 {
			end = len(h) + off
			if end < 0 {
				end = 0
			}
		}
		if start > end {
			return vm.Bool(false)
		}
		idx = lastIndexRunes(h[start:end], n)
		if idx < 0 {
			return vm.Bool(false)
		}
		idx += start
	} else {
		start := off
		if start < 0 {
			start = len(h) + start
			if start < 0 {
				start = 0
			}
		}
		if start > len(h) {
			return vm.Bool(false)
		}
		idx = indexRunes(h[start:], n)
		if idx < 0 {
			return vm.Bool(false)
		}
		idx += start
	}
	return vm.Int(int64(idx))
}

func mbCaseFn(ctx *vm.Context, upper bool) vm.Value {
	name := "mb_strtolower"
	if upper {
		name = "mb_strtoupper"
	}
	enc := mbArgEnc(ctx, 1, name)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	rs := mbDecode(arg0(ctx).ToString(), enc)
	s := string(rs)
	if upper {
		s = strings.ToUpper(s)
	} else {
		s = strings.ToLower(s)
	}
	return vm.Str(mbEncode([]rune(s), enc))
}

func mbIsApostrophe(r rune) bool {
	return r == '\'' || r == 0x2019
}

func mbTitle(rs []rune) []rune {
	var b strings.Builder
	startWord := true
	for _, r := range rs {
		switch {
		case unicode.IsLetter(r):
			if startWord {
				b.WriteString(strings.ToUpper(string(r)))
			} else {
				b.WriteString(strings.ToLower(string(r)))
			}
			startWord = false
		case unicode.IsDigit(r):
			b.WriteRune(r)
			startWord = false
		default:
			b.WriteRune(r)
			if !mbIsApostrophe(r) {
				startWord = true
			}
		}
	}
	return []rune(b.String())
}

func mbConvertCaseFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 2, "mb_convert_case")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	rs := mbDecode(arg0(ctx).ToString(), enc)
	mode := int(arg(ctx, 1).ToInt())
	var out []rune
	switch mode {
	case 0, 4: // MB_CASE_UPPER(_SIMPLE)
		out = []rune(strings.ToUpper(string(rs)))
	case 2, 6: // MB_CASE_TITLE(_SIMPLE)
		out = mbTitle(rs)
	default: // LOWER / FOLD / *_SIMPLE
		out = []rune(strings.ToLower(string(rs)))
	}
	return vm.Str(mbEncode(out, enc))
}

func mbStrSplitFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 2, "mb_str_split")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	rs := mbDecode(arg0(ctx).ToString(), enc)
	n := 1
	a := ctx.Args()
	if len(a) >= 2 && a[1].Type() != vm.NullType {
		n = int(arg(ctx, 1).ToInt())
	}
	if n < 1 {
		ctx.Throw(vm.NewThrowable("ValueError", "mb_str_split(): Argument #2 ($length) must be greater than 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	out := vm.NewArray()
	for i := 0; i < len(rs); i += n {
		end := i + n
		if end > len(rs) {
			end = len(rs)
		}
		out.Append(vm.MakeString(mbEncode(rs[i:end], enc)))
	}
	return vm.MakeArray(out)
}

func mbOrdFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 1, "mb_ord")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	rs := mbDecode(arg0(ctx).ToString(), enc)
	if len(rs) == 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "mb_ord(): Argument #1 ($string) must not be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return vm.Int(int64(rs[0]))
}

func mbChrFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 1, "mb_chr")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cp := arg0(ctx).ToInt()
	if cp < 0 || cp > 0x10FFFF {
		return vm.Bool(false)
	}
	return vm.Str(mbEncode([]rune{rune(cp)}, enc))
}

func mbSubstrCountFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 2, "mb_substr_count")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	h := mbDecode(arg0(ctx).ToString(), enc)
	n := mbDecode(arg(ctx, 1).ToString(), enc)
	if len(n) == 0 {
		return vm.Bool(false)
	}
	count := 0
	for i := 0; i+len(n) <= len(h); {
		if indexRunes(h[i:i+len(n)], n) == 0 {
			count++
			i += len(n)
		} else {
			i++
		}
	}
	return vm.Int(int64(count))
}

func mbStrstrFn(ctx *vm.Context, ci bool) vm.Value {
	name := "mb_strstr"
	if ci {
		name = "mb_stristr"
	}
	enc := mbArgEnc(ctx, 3, name)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	h := mbDecode(arg(ctx, 0).ToString(), enc)
	n := mbDecode(arg(ctx, 1).ToString(), enc)
	before := false
	a := ctx.Args()
	if len(a) >= 3 {
		before = arg(ctx, 2).ToBool()
	}
	if len(n) == 0 {
		return vm.Bool(false)
	}
	hh, nn := h, n
	if ci {
		hh = []rune(strings.ToLower(string(h)))
		nn = []rune(strings.ToLower(string(n)))
	}
	idx := indexRunes(hh, nn)
	if idx < 0 {
		return vm.Bool(false)
	}
	if before {
		return vm.Str(mbEncode(h[:idx], enc))
	}
	return vm.Str(mbEncode(h[idx:], enc))
}

func mbStrrchrFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 3, "mb_strrchr")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	h := mbDecode(arg(ctx, 0).ToString(), enc)
	n := mbDecode(arg(ctx, 1).ToString(), enc)
	before := false
	a := ctx.Args()
	if len(a) >= 3 {
		before = arg(ctx, 2).ToBool()
	}
	if len(n) == 0 {
		return vm.Bool(false)
	}
	idx := -1
	for i := len(h) - 1; i >= 0; i-- {
		if h[i] == n[0] {
			idx = i
			break
		}
	}
	if idx < 0 {
		return vm.Bool(false)
	}
	if before {
		return vm.Str(mbEncode(h[:idx], enc))
	}
	return vm.Str(mbEncode(h[idx:], enc))
}

func mbRuneWidth(r rune) int {
	if r == 0 {
		return 0
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE10 && r <= 0xFE19,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1FAFF,
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}

func mbStrwidthFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 1, "mb_strwidth")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	rs := mbDecode(arg0(ctx).ToString(), enc)
	w := 0
	for _, r := range rs {
		w += mbRuneWidth(r)
	}
	return vm.Int(int64(w))
}

func mbStrimwidthFn(ctx *vm.Context) vm.Value {
	enc := mbArgEnc(ctx, 4, "mb_strimwidth")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	rs := mbDecode(arg0(ctx).ToString(), enc)
	start := int(arg(ctx, 1).ToInt())
	width := int(arg(ctx, 2).ToInt())
	marker := ""
	a := ctx.Args()
	if len(a) >= 4 && a[3].Type() != vm.NullType {
		marker = a[3].ToString()
	}
	if start < 0 {
		start = len(rs) + start
		if start < 0 {
			start = 0
		}
	}
	if start > len(rs) {
		ctx.Throw(vm.NewThrowable("ValueError", "mb_strimwidth(): Argument #2 ($start) is out of range"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if start == len(rs) {
		return vm.Str("")
	}
	w := 0
	i := start
	for i < len(rs) {
		cw := mbRuneWidth(rs[i])
		if w+cw > width {
			break
		}
		w += cw
		i++
	}
	if i < len(rs) && marker != "" {
		mw := 0
		for _, r := range marker {
			mw += mbRuneWidth(r)
		}
		for w+mw > width && i > start {
			i--
			w -= mbRuneWidth(rs[i])
		}
		return vm.Str(mbEncode(rs[start:i], enc) + marker)
	}
	return vm.Str(mbEncode(rs[start:i], enc))
}

// --- Windows-1252 / ISO-8859-15 (однобайтовые кодировки с отличиями) ---

var cp1252High = map[byte]rune{
	0x80: 0x20AC, 0x82: 0x201A, 0x83: 0x0192, 0x84: 0x201E, 0x85: 0x2026,
	0x86: 0x2020, 0x87: 0x2021, 0x88: 0x02C6, 0x89: 0x2030, 0x8A: 0x0160,
	0x8B: 0x2039, 0x8C: 0x0152, 0x8E: 0x017D, 0x91: 0x2018, 0x92: 0x2019,
	0x93: 0x201C, 0x94: 0x201D, 0x95: 0x2022, 0x96: 0x2013, 0x97: 0x2014,
	0x98: 0x02DC, 0x99: 0x2122, 0x9A: 0x0161, 0x9B: 0x203A, 0x9C: 0x0153,
	0x9E: 0x017E, 0x9F: 0x0178,
}

var cp1252Rev = func() map[rune]byte {
	m := make(map[rune]byte, len(cp1252High))
	for b, r := range cp1252High {
		m[r] = b
	}
	return m
}()

func mbCP1252Decode(s string) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if r, ok := cp1252High[b]; ok {
			out = append(out, r)
		} else {
			out = append(out, rune(b))
		}
	}
	return out
}

func mbCP1252Encode(rs []rune) string {
	b := make([]byte, 0, len(rs))
	for _, r := range rs {
		if v, ok := cp1252Rev[r]; ok {
			b = append(b, v)
		} else if r <= 0xFF {
			b = append(b, byte(r))
		} else {
			b = append(b, '?')
		}
	}
	return string(b)
}

var latin9High = map[byte]rune{
	0xA4: 0x20AC, 0xA6: 0x0160, 0xA8: 0x0161, 0xB4: 0x017D,
	0xB8: 0x017E, 0xBC: 0x0152, 0xBD: 0x0153, 0xBE: 0x0178,
}

var latin9Rev = func() map[rune]byte {
	m := make(map[rune]byte, len(latin9High))
	for b, r := range latin9High {
		m[r] = b
	}
	return m
}()

func mbLatin9Decode(s string) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if r, ok := latin9High[b]; ok {
			out = append(out, r)
		} else {
			out = append(out, rune(b))
		}
	}
	return out
}

func mbLatin9Encode(rs []rune) string {
	b := make([]byte, 0, len(rs))
	for _, r := range rs {
		if v, ok := latin9Rev[r]; ok {
			b = append(b, v)
		} else if r <= 0xFF {
			b = append(b, byte(r))
		} else {
			b = append(b, '?')
		}
	}
	return string(b)
}

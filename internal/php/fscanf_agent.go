package php

import (
	"fmt"
	"phpvm/internal/vm"
	"strconv"
	"strings"
)

// Реализация fscanf(), перенятая из php-src ext/standard/scanf.c
// (php_sscanf_internal + ValidateFormat) и file.c (PHP_FUNCTION(fscanf)).
//
// Ключевые отличия от прежней реализации:
//   - fscanf читает из потока ровно ОДНУ строку (php_stream_get_line), затем
//     разбирает формат по этой строке; остаток строки отбрасывается;
//   - при 2-аргументной форме возвращается массив длиной в число
//     не-подавленных спецификаторов, заполненный NULL там, где не удалось
//     преобразовать;
//   - при >2 аргументах возвращается число успешных преобразований, а значения
//     пишутся в переданные по ссылке переменные;
//   - при "underflow" и нуле преобразований: NULL (2-арг) или -1 (>2-арг);
//   - при чтении за концом потока (get_line == NULL) — bool(false).

const scanMaxArgs = 255

const (
	scanNoSkip = 1 << iota
	scanSuppress
	scanUnsigned
	scanWidth
	scanSignOK
	scanNoDigits
	scanNoZero
	scanXOK
	scanPTok
	scanExpOK
)

// readScanLine читает одну строку (включая завершающий '\n'). ok=false — EOF.
func (h *fsHandle) readScanLine() (string, bool) {
	var out []byte
	for {
		b, ok := h.readByte()
		if !ok {
			break
		}
		out = append(out, b)
		if b == '\n' {
			break
		}
	}
	if len(out) == 0 {
		return "", false
	}
	return string(out), true
}

func scanSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func scanDigit(c byte) bool { return c >= '0' && c <= '9' }

func scanHexAlpha(c byte) bool {
	return (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func scanBadIndex(gotXpg bool) string {
	if gotXpg {
		return `"%n$" argument index out of range`
	}
	return "Different numbers of variable names and field specifiers"
}

// validateScanFormat повторяет ValidateFormat() из scanf.c. Возвращает текст
// ValueError (пусто, если формат корректен) и totalVars (число результатов
// для 2-аргументной формы).
func validateScanFormat(format string, numVars int) (string, int) {
	var nassign []int
	if numVars > 0 {
		nassign = make([]int, numVars)
	}
	xpgSize := 0
	objIndex := 0
	gotXpg := false
	gotSequential := false

	i := 0
	for i < len(format) {
		ch := format[i]
		i++
		if ch != '%' {
			continue
		}
		if i >= len(format) {
			gotSequential = true
			if gotXpg {
				return `cannot mix "%" and "%n$" conversion specifiers`, 0
			}
			return `Bad scan conversion character "`, 0
		}
		ch = format[i]
		i++
		if ch == '%' {
			continue
		}
		flags := 0
		if ch == '*' {
			flags |= scanSuppress
			if i >= len(format) {
				return `Bad scan conversion character "`, 0
			}
			ch = format[i]
			i++
		} else if scanDigit(ch) {
			// Возможный XPG3-спецификатор %N$.
			j := i - 1
			val := 0
			for j < len(format) && scanDigit(format[j]) {
				val = val*10 + int(format[j]-'0')
				if val > 1<<30 {
					val = 1 << 30
				}
				j++
			}
			if j < len(format) && format[j] == '$' {
				i = j + 1
				gotXpg = true
				if gotSequential {
					return `cannot mix "%" and "%n$" conversion specifiers`, 0
				}
				if val < 1 || (numVars > 0 && val > numVars) {
					return scanBadIndex(gotXpg), 0
				}
				if numVars == 0 {
					if val > scanMaxArgs {
						return scanBadIndex(gotXpg), 0
					}
					if val > xpgSize {
						xpgSize = val
					}
				}
				objIndex = val - 1
				if i >= len(format) {
					return `Bad scan conversion character "`, 0
				}
				ch = format[i]
				i++
			} else {
				gotSequential = true
				if gotXpg {
					return `cannot mix "%" and "%n$" conversion specifiers`, 0
				}
				// ch остаётся цифрой (первый символ ширины).
			}
		} else {
			gotSequential = true
			if gotXpg {
				return `cannot mix "%" and "%n$" conversion specifiers`, 0
			}
		}

		// ширина
		if scanDigit(ch) {
			j := i - 1
			for j < len(format) && scanDigit(format[j]) {
				j++
			}
			if j < len(format) {
				ch = format[j]
				i = j + 1
			} else {
				ch = 0
				i = j
			}
		}
		// модификатор длины
		if ch == 'l' || ch == 'L' || ch == 'h' {
			if i >= len(format) {
				ch = 0
			} else {
				ch = format[i]
				i++
			}
		}
		if flags&scanSuppress == 0 && numVars > 0 && objIndex >= numVars {
			return scanBadIndex(gotXpg), 0
		}
		switch ch {
		case 'n', 'd', 'D', 'i', 'o', 'x', 'X', 'u', 'f', 'e', 'E', 'g', 's', 'c':
		case '[':
			if i >= len(format) {
				return "Unmatched [ in format string", 0
			}
			if format[i] == '^' {
				i++
			}
			if i < len(format) && format[i] == ']' {
				i++
			}
			found := false
			for i < len(format) {
				if format[i] == ']' {
					i++
					found = true
					break
				}
				i++
			}
			if !found {
				return "Unmatched [ in format string", 0
			}
		default:
			if ch == 0 {
				return `Bad scan conversion character "`, 0
			}
			return fmt.Sprintf("Bad scan conversion character %q", string(ch)), 0
		}
		if flags&scanSuppress == 0 {
			if objIndex < len(nassign) {
				nassign[objIndex]++
			}
			objIndex++
		}
	}

	total := numVars
	if numVars == 0 {
		if xpgSize > 0 {
			total = xpgSize
		} else {
			total = objIndex
		}
	}
	for k := 0; k < total; k++ {
		if k < len(nassign) {
			if nassign[k] > 1 {
				return `Variable is assigned by multiple "%n$" conversion specifiers`, total
			}
			if xpgSize == 0 && nassign[k] == 0 {
				return "Variable is not assigned by any conversion specifiers", total
			}
		}
	}
	return "", total
}

func fscanfFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
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
	if len(args) < 2 {
		return vm.Bool(false)
	}
	format := arg(ctx, 1).ToString()
	numVars := len(args) - 2

	if !h.ensureRead(ctx, "fscanf") {
		return vm.Bool(false)
	}
	line, ok := h.readScanLine()
	if !ok {
		return vm.Bool(false)
	}
	msg, totalVars := validateScanFormat(format, numVars)
	if msg != "" {
		ctx.Throw(vm.NewThrowable("ValueError", msg))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return fscanfScan(ctx, line, format, numVars, totalVars)
}

func fscanfScan(ctx *vm.Context, line, format string, numVars, totalVars int) vm.Value {
	// php_sscanf_internal работает с C-строкой: NUL-terminated, поэтому всё
	// после первого NUL-байта не видно.
	if idx := strings.IndexByte(line, 0); idx >= 0 {
		line = line[:idx]
	}
	var arr []vm.Slot
	if numVars == 0 {
		arr = make([]vm.Slot, totalVars)
		for i := range arr {
			arr[i] = vm.MakeNull()
		}
	}
	objIndex := 0
	nconversions := 0
	underflow := false
	sp := 0

	assign := func(v vm.Slot) {
		if numVars > 0 {
			sscanfWriteRef(ctx, 2+objIndex, v)
		} else if objIndex < len(arr) {
			arr[objIndex] = v
		}
		objIndex++
	}

scanLoop:
	for fi := 0; fi < len(format); {
		ch := format[fi]
		fi++

		if scanSpace(ch) {
			for sp < len(line) && scanSpace(line[sp]) {
				sp++
			}
			continue
		}
		if ch != '%' {
			if sp >= len(line) {
				underflow = true
				break
			}
			sch := line[sp]
			sp++
			if ch != sch {
				break
			}
			continue
		}
		ch = format[fi]
		fi++
		if ch == '%' {
			// literal '%'
			if sp >= len(line) {
				underflow = true
				break
			}
			sch := line[sp]
			sp++
			if ch != sch {
				break
			}
			continue
		}

		flags := 0
		if ch == '*' {
			flags |= scanSuppress
			if fi >= len(format) {
				ch = 0
			} else {
				ch = format[fi]
				fi++
			}
		} else if scanDigit(ch) {
			j := fi - 1
			val := 0
			for j < len(format) && scanDigit(format[j]) {
				val = val*10 + int(format[j]-'0')
				j++
			}
			if j < len(format) && format[j] == '$' {
				fi = j + 1
				objIndex = val - 1
				if fi >= len(format) {
					ch = 0
				} else {
					ch = format[fi]
					fi++
				}
			}
		}
		width := 0
		if scanDigit(ch) {
			j := fi - 1
			for j < len(format) && scanDigit(format[j]) {
				width = width*10 + int(format[j]-'0')
				j++
			}
			flags |= scanWidth
			if j < len(format) {
				ch = format[j]
				fi = j + 1
			} else {
				ch = 0
				fi = j
			}
		}
		if ch == 'l' || ch == 'L' || ch == 'h' {
			if fi >= len(format) {
				ch = 0
			} else {
				ch = format[fi]
				fi++
			}
		}

		// Определение операции.
		op := byte(0)
		base := 0
		switch ch {
		case 'n':
			if flags&scanSuppress == 0 {
				assign(vm.MakeInt(int64(sp)))
			}
			nconversions++
			continue
		case 'd', 'D':
			op = 'i'
			base = 10
		case 'i':
			op = 'i'
			base = 0
		case 'o':
			op = 'i'
			base = 8
		case 'x', 'X':
			op = 'i'
			base = 16
		case 'u':
			op = 'i'
			base = 10
			flags |= scanUnsigned
		case 'f', 'e', 'E', 'g':
			op = 'f'
		case 's':
			op = 's'
		case 'c':
			op = 's'
			flags |= scanNoSkip
			if width == 0 {
				width = 1
			}
		case '[':
			op = '['
			flags |= scanNoSkip
		default:
			break scanLoop
		}

		if sp >= len(line) {
			underflow = true
			break
		}
		if flags&scanNoSkip == 0 {
			for sp < len(line) && scanSpace(line[sp]) {
				sp++
			}
			if sp >= len(line) {
				underflow = true
				break
			}
		}

		switch op {
		case 's':
			if width == 0 {
				width = 1 << 30
			}
			end := sp
			for end < len(line) {
				if scanSpace(line[end]) {
					break
				}
				end++
				width--
				if width == 0 {
					break
				}
			}
			assign(vm.MakeString(line[sp:end]))
			sp = end

		case '[':
			// Разбор набора (аналог BuildCharSet).
			neg := false
			if fi < len(format) && format[fi] == '^' {
				neg = true
				fi++
			}
			type rng struct{ a, b byte }
			var chars []byte
			var ranges []rng
			var start byte
			if fi < len(format) {
				start = format[fi]
			}
			c := fi
			if c < len(format) && (format[c] == ']' || format[c] == '-') {
				chars = append(chars, format[c])
				c++
			}
			for c < len(format) && format[c] != ']' {
				cur := format[c]
				var nxt byte
				if c+1 < len(format) {
					nxt = format[c+1]
				}
				switch {
				case nxt == '-':
					start = cur
				case cur == '-':
					if nxt == ']' {
						chars = append(chars, start, '-')
					} else {
						if start < nxt {
							ranges = append(ranges, rng{start, nxt})
						} else {
							ranges = append(ranges, rng{nxt, start})
						}
						c++
					}
				default:
					chars = append(chars, cur)
				}
				c++
			}
			if c < len(format) && format[c] == ']' {
				c++
			}
			fi = c
			inSet := func(c byte) bool {
				match := false
				for _, x := range chars {
					if x == c {
						match = true
						break
					}
				}
				if !match {
					for _, r := range ranges {
						if r.a <= c && c <= r.b {
							match = true
							break
						}
					}
				}
				if neg {
					return !match
				}
				return match
			}
			if width == 0 {
				width = 1 << 30
			}
			end := sp
			for end < len(line) {
				if !inSet(line[end]) {
					break
				}
				end++
				width--
				if width == 0 {
					break
				}
			}
			if end == sp {
				break scanLoop
			}
			assign(vm.MakeString(line[sp:end]))
			sp = end

		case 'f':
			buf := make([]byte, 0, 64)
			if width == 0 || width > 63 {
				width = 63
			}
			flags |= scanSignOK | scanNoDigits | scanPTok | scanExpOK
			for width > 0 && sp < len(line) {
				c := line[sp]
				add := false
				switch {
				case scanDigit(c):
					flags &^= scanSignOK | scanNoDigits
					add = true
				case c == '+' || c == '-':
					if flags&scanSignOK != 0 {
						flags &^= scanSignOK
						add = true
					}
				case c == '.':
					if flags&scanPTok != 0 {
						flags &^= scanSignOK | scanPTok
						add = true
					}
				case c == 'e' || c == 'E':
					if flags&(scanNoDigits|scanExpOK) == scanExpOK {
						flags = (flags &^ (scanExpOK | scanPTok)) | scanSignOK | scanNoDigits
						add = true
					}
				}
				if !add {
					break
				}
				buf = append(buf, c)
				sp++
				width--
				if sp >= len(line) {
					break
				}
			}
			if flags&scanNoDigits != 0 {
				if flags&scanExpOK != 0 {
					if sp >= len(line) {
						underflow = true
					}
					break scanLoop
				}
				// плохой показатель степени: откат
				n := len(buf)
				if n > 0 && (buf[n-1] == 'e' || buf[n-1] == 'E') {
					buf = buf[:n-1]
					sp--
				} else if n >= 2 {
					buf = buf[:n-2]
					sp -= 2
				} else {
					buf = buf[:0]
				}
			}
			if flags&scanSuppress == 0 {
				f, _ := strconv.ParseFloat(string(buf), 64)
				assign(vm.MakeFloat(f))
			}

		default: // 'i'
			buf := make([]byte, 0, 64)
			origBase := base
			if width == 0 || width > 63 {
				width = 63
			}
			flags |= scanSignOK | scanNoDigits | scanNoZero
			for width > 0 && sp < len(line) {
				c := line[sp]
				add := false
				switch {
				case c == '0':
					if base == 16 {
						flags |= scanXOK
					}
					if base == 0 {
						base = 8
						flags |= scanXOK
					}
					if flags&scanNoZero != 0 {
						flags &^= scanSignOK | scanNoDigits | scanNoZero
					} else {
						flags &^= scanSignOK | scanXOK | scanNoDigits
					}
					add = true
				case c >= '1' && c <= '7':
					if base == 0 {
						base = 10
					}
					flags &^= scanSignOK | scanXOK | scanNoDigits
					add = true
				case c >= '8' && c <= '9':
					if base == 0 {
						base = 10
					}
					if base <= 8 {
						break
					}
					flags &^= scanSignOK | scanXOK | scanNoDigits
					add = true
				case scanHexAlpha(c):
					if base <= 10 {
						break
					}
					flags &^= scanSignOK | scanXOK | scanNoDigits
					add = true
				case c == '+' || c == '-':
					if flags&scanSignOK != 0 {
						flags &^= scanSignOK
						add = true
					}
				case c == 'x' || c == 'X':
					if flags&scanXOK != 0 && len(buf) == 1 {
						base = 16
						flags &^= scanXOK
						add = true
					}
				}
				if !add {
					break
				}
				buf = append(buf, c)
				sp++
				width--
				if sp >= len(line) {
					break
				}
			}
			if flags&scanNoDigits != 0 {
				if sp >= len(line) {
					underflow = true
				}
				break scanLoop
			}
			if n := len(buf); n > 0 && (buf[n-1] == 'x' || buf[n-1] == 'X') {
				buf = buf[:n-1]
				sp--
			}
			if flags&scanSuppress == 0 {
				body := string(buf)
				pbase := origBase
				if pbase == 0 {
					pbase = base
				}
				strip := body
				if pbase == 16 {
					strip = strings.TrimPrefix(strings.TrimPrefix(strip, "0x"), "0X")
				}
				if flags&scanUnsigned != 0 {
					u, err := strconv.ParseUint(strings.TrimPrefix(strip, "-"), pbase, 64)
					if err != nil {
						u = 0
					}
					if strings.HasPrefix(strip, "-") {
						u = uint64(-int64(u))
					}
					val := int64(u)
					if val < 0 {
						assign(vm.MakeString(strconv.FormatUint(u, 10)))
					} else {
						assign(vm.MakeInt(val))
					}
				} else {
					v, err := strconv.ParseInt(strip, pbase, 64)
					if err != nil {
						v = 0
					}
					assign(vm.MakeInt(v))
				}
			}
		}
		nconversions++
	}

	if underflow && nconversions == 0 {
		if numVars > 0 {
			return vm.Int(-1)
		}
		return vm.MakeNull()
	}
	if numVars > 0 {
		return vm.Int(int64(nconversions))
	}
	a := vm.NewArray()
	for _, v := range arr {
		a.Append(v)
	}
	return vm.MakeArray(a)
}

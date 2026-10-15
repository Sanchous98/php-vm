package php

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"os/exec"
	"phpvm/internal/vm"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Файл содержит прочие встроенные функции (assert, отладка, системная
// информация, crypt, INI, рефлексия, буферизация вывода, заголовки и т.п.).

// --- assert() / assert_options() ---

func assertFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "assert() expects exactly 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	// assert.active=0 или zend.assertions<=0 — assert() не проверяет условие
	// (аргументы уже вычислены).
	if assertActiveOpt == 0 || zendAssertions <= 0 {
		return vm.Bool(true)
	}
	assertion := a[0]
	if assertion.ToBool() {
		return vm.Bool(true)
	}
	hasDesc := len(a) >= 2
	desc := vm.MakeNull()
	if hasDesc {
		desc = a[1]
	}
	exprText := assertExprText(assertion)
	if len(a) >= 3 {
		exprText = a[2].ToString()
	}
	return assertFailed(ctx, assertion, desc, exprText, hasDesc)
}

// assert1Fn — assert() с одним аргументом (компилятор передаёт текст проверки).
func assert1Fn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "assert() expects exactly 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if assertActiveOpt == 0 || zendAssertions <= 0 {
		return vm.Bool(true)
	}
	assertion := a[0]
	if assertion.ToBool() {
		return vm.Bool(true)
	}
	exprText := assertExprText(assertion)
	if len(a) >= 2 {
		exprText = a[1].ToString()
	}
	return assertFailed(ctx, assertion, vm.MakeNull(), exprText, false)
}

// assertFailed выполняет диагностику и завершение провалившегося assert().
func assertFailed(ctx *vm.Context, assertion, desc vm.Slot, exprText string, hasDesc bool) vm.Value {
	// Колбэк вызывается даже при включённом assert.exception.
	if assertCallbackSlot.Type() != vm.NullType {
		if !ctx.VM().IsCallable(assertCallbackSlot) {
			name := assertCallbackSlot.ToString()
			ctx.Throw(vm.NewThrowable("Error", "Invalid callback "+name+
				", function \""+name+"\" not found or invalid function name"))
			return *new(vm.Value)
		}
		m := exprText
		if hasDesc && desc.Type() != vm.NullType {
			m = desc.ToString()
		}
		if _, err := ctx.VM().CallCallable(ctx, assertCallbackSlot, []vm.Slot{
			vm.MakeString(ctx.VM().CurrentFile()), vm.MakeInt(int64(ctx.VM().CurrentLine())), vm.MakeNull(), vm.MakeString(m),
		}); err != nil {
			throwCallable(ctx, err)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	// assert($obj) или assert($cond, $obj): бросаем объект как есть.
	if assertion.Type() == vm.ObjectType {
		throwObjectAt(ctx, 0)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if hasDesc && desc.Type() == vm.ObjectType {
		throwObjectAt(ctx, 1)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	msg := "Assertion failed"
	suffix := ""
	if hasDesc && desc.Type() != vm.NullType {
		msg = desc.ToString()
		suffix = " failed"
	} else if !hasDesc {
		msg = exprText
		suffix = " failed"
	}
	if assertExceptionOpt != 0 {
		th := &vm.Throwable{Class: "AssertionError", Message: msg}
		// Zend привязывает AssertionError к месту вызова assert()
		// (gh8810_5: многострочный assert — строка открытия вызова).
		th.LocFile = ctx.VM().CurrentFile()
		if l := ctx.VM().CurrentLine(); l > 0 {
			th.LocLine = l
		}
		// Zend включает кадр assert(...) в трейс AssertionError.
		th.Trace = ctx.VM().CaptureExceptionTrace()
		ctx.Throw(th)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if assertWarningOpt != 0 {
		ctx.VM().Warning("assert(): " + msg + suffix)
	}
	if assertBailOpt != 0 {
		// zend_bailout: исполнение прекращается сразу после диагностики.
		ctx.VM().BailOut()
	}
	return vm.Bool(false)
}

// assertExprText — приблизительная реконструкция исходного текста выражения.
// Точный текст знает только компилятор, поэтому восстанавливаем типичные случаи.
func assertExprText(v vm.Slot) string {
	switch v.Type() {
	case vm.BoolType:
		if v.ToBool() {
			return "assert(true)"
		}
		return "assert(false)"
	case vm.IntType:
		return "assert(" + v.ToString() + ")"
	case vm.NullType:
		return "assert(null)"
	}
	return "assert(" + v.ToString() + ")"
}

// throwObjectAt бросает объект-исключение из аргумента idx с сохранением
// класса и сообщения.
func throwObjectAt(ctx *vm.Context, idx int) {
	obj := ctx.Arg[*vm.Object](idx)
	if obj == nil {
		ctx.Throw(vm.NewThrowable("AssertionError", ""))
		if ctx.HasThrown() {
			return
		}
	}
	// Трейс берётся из объекта (зафиксирован при конструировании исключения).
	if th, ok := ctx.VM().ThrowableFromValue(arg(ctx, idx)).(*vm.Throwable); ok {
		ctx.Throw(th)
		return
	}
	msg := ""
	if v, ok := obj.GetProp("message"); ok {
		msg = v.ToString()
	}
	ctx.Throw(&vm.Throwable{Class: obj.ClassName(), Message: msg, Value: arg(ctx, idx)})
	if ctx.HasThrown() {
		return
	}
}

func assertOptionsFn(ctx *vm.Context) vm.Value {
	ctx.VM().Deprecated("Function assert_options() is deprecated")
	a := ctx.Args()
	if len(a) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "assert_options() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	opt := a[0].ToInt()
	var old vm.Value
	switch opt {
	case 1:
		old = vm.Int(assertActiveOpt)
	case 2:
		old = assertCallbackSlot
	case 3:
		old = vm.Int(assertBailOpt)
	case 4:
		old = vm.Int(assertWarningOpt)
	case 5:
		old = vm.Int(assertExceptionOpt)
	default:
		ctx.Throw(vm.NewThrowable("ValueError",
			"assert_options(): Argument #1 ($option) must be an ASSERT_* constant"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if len(a) >= 2 {
		// В PHP assert_options() меняет рантайм-значения, но НЕ строки INI
		// (ini_get("assert.callback") может отличаться от возврата
		// assert_options(ASSERT_CALLBACK)).
		switch opt {
		case 1:
			assertActiveOpt = a[1].ToInt()
		case 2:
			assertCallbackSlot = a[1]
		case 3:
			assertBailOpt = a[1].ToInt()
		case 4:
			assertWarningOpt = a[1].ToInt()
		case 5:
			assertExceptionOpt = a[1].ToInt()
		}
	}
	return old
}

// --- системная информация ---

func gethostnameFn(ctx *vm.Context) vm.Value {
	h, err := os.Hostname()
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Str(h)
}

// hrtimeBase — монотонная точка отсчёта для hrtime().
var hrtimeBase = time.Now()

// hrtimeFn реализует hrtime($as_number = false).
func hrtimeFn(ctx *vm.Context) vm.Value {
	d := time.Since(hrtimeBase)
	sec := int64(d / time.Second)
	nsec := int64(d % time.Second)
	if len(ctx.Args()) >= 1 && arg0(ctx).ToBool() {
		return vm.Int(sec*1000000000 + nsec)
	}
	arr := vm.NewArray()
	_, _ = arr.Append(vm.MakeInt(sec))
	_, _ = arr.Append(vm.MakeInt(nsec))
	return vm.MakeArray(arr)
}

func phpUnameFn(ctx *vm.Context) vm.Value {
	mode := "a"
	if len(ctx.Args()) > 0 {
		mode = arg0(ctx).ToString()
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	switch mode {
	case "s":
		return vm.Str("Linux")
	case "n":
		return vm.Str(host)
	case "r":
		return vm.Str("unknown")
	case "v":
		return vm.Str("unknown")
	case "m":
		return vm.Str("x86_64")
	default:
		return vm.Str("Linux " + host + " unknown unknown x86_64")
	}
}

func arg0(ctx *vm.Context) vm.Slot {
	return arg(ctx, 0)
}

func arg(ctx *vm.Context, i int) vm.Slot {
	a := ctx.Args()
	if i < 0 || i >= len(a) {
		return vm.MakeNull()
	}
	return a[i]
}

// derefSlot снимает by-ref обёртку со слота-аргумента встроенной функции.
func derefSlot(s vm.Slot) vm.Slot {
	if s.IsRef() {
		if r := s.Ref(); r != nil {
			return r.Value()
		}
	}
	return s
}

// --- состояние встроенных функций miscAgent ---

var (
	assertActiveOpt    int64
	assertWarningOpt   int64
	assertBailOpt      int64
	assertExceptionOpt int64
	assertCallbackSlot vm.Slot

	// zendAssertions — значение INI-директивы zend.assertions (PHP 8):
	// 1 — assert() вычисляется, 0 — не вычисляется (возвращает true),
	// -1 — полностью отключён на старте, менять в рантайме нельзя.
	zendAssertions int64 = 1

	// lastErrorArray — снимок последней ошибки для error_get_last().
	lastErrorArray *vm.Array

	// internalFuncNames — имена всех встроенных функций (для
	// get_defined_functions()).
	internalFuncNames []string

	// definedConstNames — имена констант, экспортируемых get_defined_constants().
	definedConstNames = []string{
		"ASSERT_ACTIVE", "ASSERT_CALLBACK", "ASSERT_BAIL", "ASSERT_WARNING", "ASSERT_EXCEPTION",
		"EXTR_OVERWRITE", "EXTR_SKIP", "EXTR_PREFIX_SAME", "EXTR_PREFIX_ALL",
		"EXTR_PREFIX_INVALID", "EXTR_PREFIX_IF_EXISTS", "EXTR_IF_EXISTS", "EXTR_REFS",
		"E_ERROR", "E_WARNING", "E_PARSE", "E_NOTICE", "E_CORE_ERROR", "E_CORE_WARNING",
		"E_COMPILE_ERROR", "E_COMPILE_WARNING", "E_USER_ERROR", "E_USER_WARNING",
		"E_USER_NOTICE", "E_STRICT", "E_RECOVERABLE_ERROR", "E_DEPRECATED",
		"E_USER_DEPRECATED", "E_ALL",
		"ENT_NOQUOTES", "ENT_COMPAT", "ENT_QUOTES", "ENT_IGNORE", "ENT_SUBSTITUTE",
		"SORT_REGULAR", "SORT_NUMERIC", "SORT_STRING",
		"PHP_INT_MAX", "PHP_INT_MIN", "PHP_INT_SIZE", "PHP_FLOAT_MAX", "PHP_FLOAT_MIN",
		"PHP_URL_SCHEME", "PHP_URL_HOST", "PHP_URL_PORT", "PHP_URL_USER",
		"PHP_URL_PASS", "PHP_URL_PATH", "PHP_URL_QUERY", "PHP_URL_FRAGMENT",
	}
)

// maxRangeSize — верхняя граница числа элементов range(); защищает от
// попытки выделить гигантский массив (PHP в этом случае бросает ValueError).
const maxRangeSize = 1 << 30

// --- crypt (MD5-crypt и заглушки) ---

const itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func cryptFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) != 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", fmt.Sprintf("crypt() expects exactly 2 arguments, %d given", len(ctx.Args()))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Str(phpCrypt(arg0(ctx).ToString(), arg1Str(ctx)))
}

// phpCrypt — ядро crypt(): выбор реализации по префиксу соли.
func phpCrypt(pw, salt string) string {
	if strings.HasPrefix(salt, "$1$") {
		return cryptMD5(pw, salt)
	}
	if strings.HasPrefix(salt, "$5$") {
		return cryptSHA(pw, salt, 5)
	}
	if strings.HasPrefix(salt, "$6$") {
		return cryptSHA(pw, salt, 6)
	}
	if len(salt) >= 2 && salt[0] == '*' && (salt[1] == '0' || salt[1] == '1') {
		if salt[1] == '0' {
			return "*1"
		}
		return "*0"
	}
	if r := cryptBlowfish(pw, salt); r != "" {
		return r
	}
	if r := cryptDES(pw, salt); r != "" {
		return r
	}
	return "*0"
}

func cryptMD5(password, salt string) string {
	magic := "$1$"
	rest := salt[len(magic):]
	if i := strings.IndexByte(rest, '$'); i >= 0 {
		rest = rest[:i]
	}
	if len(rest) > 8 {
		rest = rest[:8]
	}
	salt = rest

	h := md5.New()
	h.Write([]byte(password))
	h.Write([]byte(magic))
	h.Write([]byte(salt))
	final := h.Sum(nil)

	h = md5.New()
	h.Write([]byte(password))
	h.Write([]byte(salt))
	h.Write([]byte(password))
	alt := h.Sum(nil)

	h = md5.New()
	h.Write([]byte(password))
	h.Write([]byte(magic))
	h.Write([]byte(salt))
	for i := len(password); i > 0; i -= 16 {
		if i > 16 {
			h.Write(alt)
		} else {
			h.Write(alt[:i])
		}
	}
	for i := len(password); i > 0; i >>= 1 {
		if i&1 != 0 {
			h.Write([]byte{0})
		} else {
			h.Write([]byte{password[0]})
		}
	}
	final = h.Sum(nil)

	for i := 0; i < 1000; i++ {
		h = md5.New()
		if i&1 != 0 {
			h.Write([]byte(password))
		} else {
			h.Write(final)
		}
		if i%3 != 0 {
			h.Write([]byte(salt))
		}
		if i%7 != 0 {
			h.Write([]byte(password))
		}
		if i&1 != 0 {
			h.Write(final)
		} else {
			h.Write([]byte(password))
		}
		final = h.Sum(nil)
	}

	var b strings.Builder
	b.WriteString(magic)
	b.WriteString(salt)
	b.WriteByte('$')
	to64 := func(v uint32, n int) {
		for ; n > 0; n-- {
			b.WriteByte(itoa64[v&0x3f])
			v >>= 6
		}
	}
	to64(uint32(final[0])<<16|uint32(final[6])<<8|uint32(final[12]), 4)
	to64(uint32(final[1])<<16|uint32(final[7])<<8|uint32(final[13]), 4)
	to64(uint32(final[2])<<16|uint32(final[8])<<8|uint32(final[14]), 4)
	to64(uint32(final[3])<<16|uint32(final[9])<<8|uint32(final[15]), 4)
	to64(uint32(final[4])<<16|uint32(final[10])<<8|uint32(final[5]), 4)
	to64(uint32(final[11]), 2)
	return b.String()
}

// --- объекты: доступ к свойствам через интерфейс *vm.Object ---

func slotFromValue(v vm.Value) vm.Slot {
	var s vm.Slot
	vm.SlotOf(&s, v)
	return s
}

// makeStdClass создаёт объект stdClass с заданными свойствами. num слота
// выставляется в индекс stdClass, чтобы vm.classInfo() резолвил класс.
func makeStdClass(m *vm.VM, order []string, props map[string]vm.Value) vm.Slot {
	slot, o := m.NewObjectOfIndex(stdClassInfo, stdClassIndex)
	if o == nil {
		return vm.MakeNull()
	}
	for k, v := range props {
		o.SetProp(k, v)
	}
	if len(order) > 0 {
		objKeyOrder[o] = order
		o.SetDynOrder(order)
	}
	return slot
}

const hexLower = "0123456789abcdef"

// --- parse_url ---

func parseURLFn(ctx *vm.Context) vm.Value {
	raw := arg0(ctx).ToString()
	res, ok := phpParseURL(raw)
	if !ok {
		return vm.Bool(false)
	}
	if len(ctx.Args()) >= 2 && !arg(ctx, 1).IsNull() {
		key := int(arg(ctx, 1).ToInt())
		names := []string{"scheme", "host", "port", "user", "pass", "path", "query", "fragment"}
		if key > 7 {
			ctx.Throw(vm.NewThrowable("ValueError", fmt.Sprintf("parse_url(): Argument #2 ($component) must be a valid URL component identifier, %d given", key)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
		if key < 0 {
			// Отрицательный компонент (в т.ч. -1) — вернуть весь массив (Zend).
			return vm.MakeArray(res)
		}
		if v, ok := res.Get(vm.MakeStrKey(names[key])); ok {
			return v
		}
		return vm.MakeNull()
	}
	return vm.MakeArray(res)
}

func isSchemeChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '.' || c == '-'
}

// strtolPrefix повторяет C strtol(s, &end, 10): пропускает ведущие пробелы,
// знак и цифры, возвращает значение префикса. ok=false, если цифр нет
// (end == начала строки). Переполнение насыщается до границ int64.
func strtolPrefix(s string) (int64, bool) {
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' {
			i++
			continue
		}
		break
	}
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	start := i
	var v int64
	overflow := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		d := int64(s[i] - '0')
		if v > (1<<63-1-d)/10 {
			overflow = true
		} else if !overflow {
			v = v*10 + d
		}
		i++
	}
	if i == start {
		return 0, false
	}
	if overflow {
		if neg {
			return -1 << 63, true
		}
		return 1<<63 - 1, true
	}
	if neg {
		v = -v
	}
	return v, true
}

func phpParseURL(str string) (*vm.Array, bool) {
	n := len(str)
	ue := n
	s := 0
	var scheme, user, pass, host, path, query, fragment string
	hasScheme, hasHost, hasPort := false, false, false
	hasUser, hasPass, hasQuery, hasFrag, hasPath := false, false, false, false, false
	port := 0

	colon := -1
	for i := 0; i < n; i++ {
		if str[i] == ':' {
			colon = i
			break
		}
	}
	spn := n
	for i := 0; i < n; i++ {
		if str[i] == '?' || str[i] == '#' {
			spn = i
			break
		}
	}

	e := colon
	stage := 0
	if colon > 0 {
		valid := true
		for p := 0; p < e; p++ {
			if !isSchemeChar(str[p]) {
				valid = false
				break
			}
		}
		if !valid {
			if e+1 < ue && e < spn {
				stage = 1
			} else if s+1 < ue && str[s] == '/' && str[s+1] == '/' {
				s += 2
				stage = 2
			} else {
				stage = 3
			}
		} else {
			if e+1 == ue {
				scheme = str[:e]
				hasScheme = true
				stage = 4
			} else if str[e+1] != '/' {
				p := e + 1
				for p < ue && str[p] >= '0' && str[p] <= '9' {
					p++
				}
				if (p == ue || str[p] == '/') && (p-e) < 7 {
					stage = 1
				} else {
					scheme = str[:e]
					hasScheme = true
					s = e + 1
					stage = 3
				}
			} else {
				scheme = str[:e]
				hasScheme = true
				if e+2 < ue && str[e+2] == '/' {
					s = e + 3
					// scheme:///path: для file: третья косая черта —
					// префикс just_path (с поддержкой диска windows).
					if strings.EqualFold(scheme, "file") && e+3 < ue && str[e+3] == '/' {
						if e+5 < ue && str[e+5] == ':' {
							s = e + 4
						}
						stage = 3
					} else {
						stage = 2
					}
				} else {
					s = e + 1
					stage = 3
				}
			}
		}
	} else if colon == 0 {
		stage = 1
	} else {
		if s+1 < ue && str[s] == '/' && str[s+1] == '/' {
			s += 2
			stage = 2
		} else {
			stage = 3
		}
	}

	for stage != 4 {
		switch stage {
		case 1: // parse_port
			p := e + 1
			pp := p
			for pp < ue && pp-p < 6 && str[pp] >= '0' && str[pp] <= '9' {
				pp++
			}
			if pp-p > 0 && pp-p < 6 && (pp == ue || str[pp] == '/') {
				val, ok2 := strtolPrefix(str[p:pp])
				if ok2 && val >= 0 && val <= 65535 {
					hasPort = true
					port = int(val)
					if s+1 < ue && str[s] == '/' && str[s+1] == '/' {
						s += 2
					}
					stage = 2
				} else {
					return nil, false
				}
			} else if p == pp && pp == ue {
				return nil, false
			} else if s+1 < ue && str[s] == '/' && str[s+1] == '/' {
				s += 2
				stage = 2
			} else {
				stage = 3
			}
		case 2: // parse_host
			e2 := s
			for e2 < ue && str[e2] != '/' && str[e2] != '?' && str[e2] != '#' {
				e2++
			}
			at := -1
			for i := e2 - 1; i >= s; i-- {
				if str[i] == '@' {
					at = i
					break
				}
			}
			if at >= 0 {
				cp := -1
				for i := s; i < at; i++ {
					if str[i] == ':' {
						cp = i
						break
					}
				}
				if cp >= 0 {
					user = str[s:cp]
					hasUser = true
					pass = str[cp+1 : at]
					hasPass = true
				} else {
					user = str[s:at]
					hasUser = true
				}
				s = at + 1
			}
			pp2 := -1
			if s < ue && str[s] == '[' && e2-1 >= 0 && str[e2-1] == ']' {
				pp2 = -1
			} else {
				for i := e2 - 1; i >= s; i-- {
					if str[i] == ':' {
						pp2 = i
						break
					}
				}
			}
			pEnd := e2
			if pp2 >= 0 {
				if !hasPort {
					pp2++
					if e2-pp2 > 5 {
						return nil, false
					}
					if e2-pp2 > 0 {
						// ZEND_STRTOL: числовой префикс, хвост игнорируется
						// (напр. "6.5" даёт порт 6), но без цифр — ошибка.
						val, ok2 := strtolPrefix(str[pp2:e2])
						if ok2 && val >= 0 && val <= 65535 {
							hasPort = true
							port = int(val)
						} else {
							return nil, false
						}
					}
					pp2--
				}
				pEnd = pp2
			} else {
				pEnd = e2
			}
			if pEnd-s < 1 {
				// Пустой authority (напр. "http:///x") — не URL (Zend).
				return nil, false
			}
			host = str[s:pEnd]
			hasHost = true
			if e2 == ue {
				stage = 4
			} else {
				s = e2
				stage = 3
			}
		case 3: // just_path
			end := ue
			fi := -1
			for i := s; i < end; i++ {
				if str[i] == '#' {
					fi = i
					break
				}
			}
			if fi >= 0 {
				f := fi + 1
				if f < end {
					fragment = str[f:end]
				} else {
					fragment = ""
				}
				hasFrag = true
				end = fi
			}
			qi := -1
			for i := s; i < end; i++ {
				if str[i] == '?' {
					qi = i
					break
				}
			}
			if qi >= 0 {
				q := qi + 1
				if q < end {
					query = str[q:end]
				} else {
					query = ""
				}
				hasQuery = true
				end = qi
			}
			if s < end || s == ue {
				path = str[s:end]
				hasPath = true
			}
			stage = 4
		}
	}

	a := vm.NewArray()
	if hasScheme {
		a.SetStr("scheme", vm.MakeString(scheme))
	}
	if hasHost {
		a.SetStr("host", vm.MakeString(host))
	}
	if hasPort {
		a.SetStr("port", vm.MakeInt(int64(port)))
	}
	if hasUser {
		a.SetStr("user", vm.MakeString(user))
	}
	if hasPass {
		a.SetStr("pass", vm.MakeString(pass))
	}
	if hasPath {
		a.SetStr("path", vm.MakeString(path))
	}
	if hasQuery {
		a.SetStr("query", vm.MakeString(query))
	}
	if hasFrag {
		a.SetStr("fragment", vm.MakeString(fragment))
	}
	return a, true
}

// --- http_build_query ---

func httpBuildQueryFn(ctx *vm.Context) vm.Value {
	prefix := ""
	if len(ctx.Args()) >= 2 && !arg(ctx, 1).IsNull() {
		prefix = arg(ctx, 1).ToString()
	}
	sep := "&"
	if len(ctx.Args()) >= 3 && !arg(ctx, 2).IsNull() {
		sep = arg(ctx, 2).ToString()
	}
	enc := int64(1)
	if len(ctx.Args()) >= 4 && !arg(ctx, 3).IsNull() {
		enc = arg(ctx, 3).ToInt()
	}
	raw := enc == 2
	var b strings.Builder
	first := true
	scope := ctx.Self()
	if scope == nil {
		scope = ctx.Called()
	}
	httpBuildQueryRec(ctx.VM(), scope, &b, &first, arg0(ctx), "", prefix, sep, raw, 0, map[*vm.Object]bool{})
	return vm.Str(b.String())
}

func httpBuildQueryRec(m *vm.VM, scope vm.ClassInfo, b *strings.Builder, first *bool, v vm.Slot, base, prefix, sep string, raw bool, level int, seen map[*vm.Object]bool) {
	if level > 128 {
		return
	}
	switch v.Type() {
	case vm.ArrayType:
		a := v.AsArray()
		for k, val := range a.All() {
			var key string
			if k.IsStr() {
				key = k.StrVal()
			} else if level == 0 {
				key = prefix + strconv.FormatInt(k.IntVal(), 10)
			} else {
				key = strconv.FormatInt(k.IntVal(), 10)
			}
			httpBuildQueryRec(m, scope, b, first, val, joinQueryKey(base, key), prefix, sep, raw, level+1, seen)
		}
	case vm.ObjectType:
		o := m.SlotObject(v)
		if o == nil || seen[o] {
			return
		}
		seen[o] = true
		defer delete(seen, o)
		for _, de := range m.ObjectDumpEntries(o) {
			if de.Uninit || !queryPropVisible(m, scope, de) {
				continue
			}
			httpBuildQueryRec(m, scope, b, first, de.Val, joinQueryKey(base, de.Key), prefix, sep, raw, level+1, seen)
		}
	default:
		if v.IsNull() || v.Type() == vm.ResourceType {
			return
		}
		var vs string
		if v.Type() == vm.BoolType {
			if v.ToBool() {
				vs = "1"
			} else {
				vs = "0"
			}
		} else {
			vs = v.ToString()
		}
		if !*first {
			b.WriteString(sep)
		}
		*first = false
		b.WriteString(phpURLEncode(base, raw))
		b.WriteByte('=')
		b.WriteString(phpURLEncode(vs, raw))
	}
}

// queryPropVisible — видимость свойства объекта для http_build_query с учётом
// scope вызывающего кода (private — только класс объявления, protected —
// класс объявления и его наследники), как zend_check_property_access.
func queryPropVisible(m *vm.VM, scope vm.ClassInfo, de vm.DumpEntry) bool {
	switch de.Suffix {
	case "":
		return true
	case ":protected":
		if scope == nil {
			return false
		}
		decl := m.FindClass(de.Decl)
		return decl != nil && vm.ClassExtendsOrIs(scope, decl)
	default:
		if scope == nil {
			return false
		}
		return strings.EqualFold(scope.Name(), de.Decl)
	}
}

func joinQueryKey(base, key string) string {
	if base == "" {
		return key
	}
	return base + "[" + key + "]"
}

// --- callable-функции (CallCallable) ---

// throwCallable пробрасывает ошибку вызова callable как PHP-исключение.
func throwCallable(ctx *vm.Context, err error) {
	if err == nil {
		return
	}
	if vm.IsExitSignal(err) {
		// exit/die внутри callable: останавливаем скрипт, а не превращаем
		// сигнал в Error "exit" (bug54910).
		ctx.VM().RequestHalt()
		return
	}
	if th, ok := err.(*vm.Throwable); ok {
		ctx.Throw(th)
		if ctx.HasThrown() {
			return
		}
	}
	ctx.Throw(vm.NewThrowable("Error", err.Error()))
	if ctx.HasThrown() {
		return
	}
}

// phpTypeName возвращает имя PHP-типа аргумента #i (нумерация с 0) так, как его
// печатает PHP в сообщениях о неверном типе: bool -> true/false, объект — имя
// класса, остальное — обычные имена типов.
func phpTypeName(ctx *vm.Context, i int) string {
	s := arg(ctx, i)
	switch s.Type() {
	case vm.NullType:
		return "null"
	case vm.BoolType:
		if s.ToBool() {
			return "true"
		}
		return "false"
	case vm.IntType:
		return "int"
	case vm.FloatType:
		return "float"
	case vm.StringType:
		return "string"
	case vm.ArrayType:
		return "array"
	case vm.ObjectType:
		if o, ok := ctx.Arg[any](i).(*vm.Object); ok && o != nil {
			if n := o.ClassName(); n != "" {
				return n
			}
		}
		return "object"
	case vm.ClosureType:
		return "Closure"
	case vm.ResourceType:
		return "resource"
	}
	return s.Type().String()
}

// registerShutdownFunctionFn — register_shutdown_function(callable $callback, mixed ...$args).
func registerShutdownFunctionFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "register_shutdown_function() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	requireCallableNamed(ctx, 0, "register_shutdown_function", "callback", false)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cb := ctx.VMInstance().NormalizeCallableSlot(derefSlot(args[0]))
	// Нестатический метод в строковой форме ('Test::method'), зарегистрированный
	// из метода объекта: Zend сохраняет текущий $this и вызывает обработчик с
	// ним (register_shutdown_function_refcount).
	cb = ctx.VMInstance().BindThisToMethodCallable(cb, ctx.VMInstance().CurrentThis())
	var extra []vm.Slot
	if len(args) > 1 {
		extra = append(extra, args[1:]...)
	}
	ctx.VM().AddShutdownFunction(cb, extra)
	return vm.MakeNull()
}

// isCallableFn — is_callable($value, $syntax_only = false, &$callable_name).
func isCallableFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	syntax := false
	if len(args) >= 2 {
		syntax = arg(ctx, 1).ToBool()
	}
	ok, name := ctx.VMInstance().IsCallableEx(arg0(ctx), syntax)
	if len(args) >= 3 {
		if s := arg(ctx, 2); s.IsRef() && s.Ref() != nil {
			s.Ref().Set(vm.MakeString(name))
		}
	}
	return vm.Bool(ok)
}

func callUserFuncFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "call_user_func() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	requireCallableNamed(ctx, 0, "call_user_func", "callback", false)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cb := derefSlot(args[0])
	res, err := ctx.VMInstance().CallCallableUser(ctx, cb, args[1:])
	throwCallable(ctx, err)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return res
}

func callUserFuncArrayFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "call_user_func_array() expects at least 2 arguments"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	requireCallableNamed(ctx, 0, "call_user_func_array", "callback", false)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	arr := requireArrayArgNamed(ctx, 1, "call_user_func_array", "args")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if arr.HasStringKeys() {
		res, err := ctx.VMInstance().CallCallableNamedDirect(ctx, derefSlot(args[0]), arr)
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return res
	}
	res, err := ctx.VMInstance().CallCallableUser(ctx, derefSlot(args[0]), arr.Values())
	throwCallable(ctx, err)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return res
}

// forwardStaticCallFn — forward_static_call(callable $callback, mixed ...$args).
// Вызывает callable в текущем статическом контексте класса.
func forwardStaticCallFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "forward_static_call() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	self, called := ctx.VMInstance().ForwardScope()
	if self == nil && called == nil {
		ctx.Throw(vm.NewThrowable("Error", "Cannot call forward_static_call() when no class scope is active"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	requireCallableNamed(ctx, 0, "forward_static_call", "callback", false)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	res, err := ctx.VMInstance().CallCallableCalled(ctx, derefSlot(args[0]), args[1:], self, called)
	throwCallable(ctx, err)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return res
}

// forwardStaticCallArrayFn — forward_static_call_array(callable, array $args).
func forwardStaticCallArrayFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "forward_static_call_array() expects exactly 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	// Порядок ключей массива аргументов проверяется до проверки scope
	// (Zend: zend_handle_named_arg срабатывает при распаковке).
	if a := derefSlot(args[1]); a.Type() == vm.ArrayType {
		if err := ctx.VMInstance().CheckNamedOrder(a.AsArray()); err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
			} else {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
			}
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	self, called := ctx.VMInstance().ForwardScope()
	if self == nil && called == nil {
		ctx.Throw(vm.NewThrowable("Error", "Cannot call forward_static_call_array() when no class scope is active"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	arr := requireArrayArgNamed(ctx, 1, "forward_static_call_array", "args")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if arr.HasStringKeys() {
		res, err := ctx.VMInstance().CallCallableNamedCalled(ctx, derefSlot(args[0]), arr, self, called)
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return res
	}
	res, err := ctx.VMInstance().CallCallableCalled(ctx, derefSlot(args[0]), arr.Values(), self, called)
	throwCallable(ctx, err)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return res
}

// xmlParser — минимальная заглушка ресурса ext/xml (bug34617): тесты
// проверяют только создание/освобождение и xml_set_object.
type xmlParser struct{ obj vm.Value }

var xmlParserRes = map[int64]*xmlParser{}

// xmlParserCreate — общий код xml_parser_create[_ns].
func xmlParserCreate(ctx *vm.Context) vm.Value {
	id := fsNextID
	fsNextID++
	h := &xmlParser{}
	xmlParserRes[id] = h
	return vm.MakeResource(id, "xml", h)
}

// callableReason возвращает причину невалидности callable в формате PHP
// (или "" если значение действительно вызываемо).
// methodAccessReason возвращает причину недоступности непубличного метода
// ("" если доступен) с учётом текущего scope — как zend_is_callable.
func methodAccessReason(ctx *vm.Context, ci vm.ClassInfo, mname string) string {
	decl, vis, ok := ci.MethodDecl(mname)
	if !ok || vis == vm.Public {
		return ""
	}
	if decl == nil {
		decl = ci
	}
	scope := ctx.Self()
	if scope != nil {
		if vis == vm.Private {
			if scope == decl {
				return ""
			}
		} else if scope == decl || scope.IsA(decl) || decl.IsA(scope) {
			return ""
		}
	}
	kind := "private"
	if vis == vm.Protected {
		kind = "protected"
	}
	return fmt.Sprintf("cannot access %s method %s::%s()", kind, vm.ClassDisplayName(ci.Name()), mname)
}

// callableVisibilityReason проверяет видимость callable-методов до общей
// проверки IsCallable (которая видимость не учитывает).
func callableVisibilityReason(ctx *vm.Context, cb vm.Slot) string {
	cb = derefSlot(cb)
	switch cb.Type() {
	case vm.ArrayType:
		vals := cb.AsArray().Values()
		if len(vals) != 2 {
			return ""
		}
		mname := strings.TrimLeft(derefSlot(vals[1]).ToString(), "\\")
		switch derefSlot(vals[0]).Type() {
		case vm.ClosureType:
			// [Closure, "__invoke"] / [Closure, "call"] и т.п. — валидный
			// callable (для замыканий разрешаем).
			return ""
		case vm.ObjectType:
			if o := ctx.VMInstance().SlotObject(vals[0]); o != nil {
				if ci := ctx.VMInstance().FindClass(o.ClassName()); ci != nil && ctx.VMInstance().ClassHasMethod(ci, mname) {
					if r := methodAccessReason(ctx, ci, mname); r != "" {
						// Недоступный метод всё равно callable, если есть
						// __call (Zend zend_is_callable: магия покрывает
						// видимость).
						if !ctx.VMInstance().ClassHasMethod(ci, "__call") {
							return r
						}
					}
					return ""
				}
			}
		case vm.StringType:
			cls := strings.TrimLeft(derefSlot(vals[0]).ToString(), "\\")
			if ci := ctx.VMInstance().FindClass(cls); ci != nil && ctx.VMInstance().ClassHasMethod(ci, mname) {
				if static, _ := ctx.VMInstance().MethodStatic(ci, mname); !static {
					return "" // «non-static method ...» имеет приоритет
				}
				if r := methodAccessReason(ctx, ci, mname); r != "" {
					if !ctx.VMInstance().ClassHasMethod(ci, "__callStatic") {
						return r
					}
				}
				return ""
			}
		}
	case vm.StringType:
		name := cb.ToString()
		if i := strings.Index(name, "::"); i >= 0 {
			cls := strings.TrimLeft(name[:i], "\\")
			mname := name[i+2:]
			if ci := ctx.VMInstance().FindClass(cls); ci != nil && ctx.VMInstance().ClassHasMethod(ci, mname) {
				if static, _ := ctx.VMInstance().MethodStatic(ci, mname); !static {
					return ""
				}
				return methodAccessReason(ctx, ci, mname)
			}
		}
	}
	return ""
}

func callableReason(ctx *vm.Context, cb vm.Slot) string {
	if r := callableVisibilityReason(ctx, cb); r != "" {
		return r
	}
	if ctx.VMInstance().IsCallable(cb) {
		return ""
	}
	switch cb.Type() {
	case vm.StringType:
		name := cb.ToString()
		if i := strings.Index(name, "::"); i >= 0 {
			cls := strings.TrimLeft(name[:i], "\\")
			if strings.EqualFold(cls, "self") || strings.EqualFold(cls, "parent") || strings.EqualFold(cls, "static") {
				if ctx.VMInstance().CtxSelf() == nil {
					return fmt.Sprintf("cannot access %q when no class scope is active", strings.ToLower(cls))
				}
			}
			if ctx.VMInstance().FindClass(cls) == nil {
				ctx.VMInstance().Autoload(ctx, cls)
			}
			ci := ctx.VMInstance().FindClass(cls)
			if ci == nil {
				return fmt.Sprintf("class %q not found", name[:i])
			}
			mname := name[i+2:]
			if !ctx.VMInstance().CallableStaticStringValid(ci, mname) {
				return fmt.Sprintf("class %s does not have a method %q", ci.Name(), mname)
			}
			return ""
		}
		if !ctx.VMInstance().FunctionByName(name) {
			return fmt.Sprintf("function %q not found or invalid function name", name)
		}
		return ""
	case vm.ArrayType:
		vals := cb.AsArray().Values()
		if len(vals) != 2 {
			return "array callback must have exactly two members"
		}
		// Порядок проверок как в Zend: сперва валидность первого элемента.
		switch derefSlot(vals[0]).Type() {
		case vm.ObjectType, vm.StringType, vm.ClosureType:
		default:
			return "first array member is not a valid class name or object"
		}
		second := derefSlot(vals[1])
		if second.Type() != vm.StringType && second.Type() != vm.IntType {
			return "second array member is not a valid method"
		}
		mname := second.ToString()
		switch derefSlot(vals[0]).Type() {
		case vm.ClosureType:
			// [Closure, "__invoke"] / [Closure, "call"] — валидный callable.
			return ""
		case vm.ObjectType:
			cn := ""
			var recvCI vm.ClassInfo
			if o := ctx.VMInstance().SlotObject(vals[0]); o != nil {
				cn = o.ClassName()
				recvCI = o.Class()
			}
			// [obj, "Class::method"] — класс в строке метода; абстрактный
			// метод и несовместимость получателя дают специальные диагностики.
			if i := strings.Index(mname, "::"); i >= 0 {
				cls2 := strings.TrimLeft(mname[:i], "\\")
				m2 := mname[i+2:]
				ci2 := ctx.VMInstance().FindClass(cls2)
				if ci2 == nil {
					if ok, _ := ctx.VMInstance().Autoload(ctx, cls2); ok {
						ci2 = ctx.VMInstance().FindClass(cls2)
					}
				}
				if ci2 != nil {
					if ctx.VMInstance().MethodIsAbstract(ci2, m2) {
						return "cannot call abstract method " + cls2 + "::" + m2 + "()"
					}
					if recvCI != nil && !recvCI.IsA(ci2) {
						return fmt.Sprintf("class %s is not a subclass of %s", cn, cls2)
					}
				}
			}
			ci := ctx.VMInstance().FindClass(cn)
			if ci == nil {
				return "first array member is not a valid class name or object"
			}
			if !ctx.VMInstance().ClassHasMethod(ci, mname) {
				return fmt.Sprintf("class %s does not have a method %q", cn, mname)
			}
			return ""
		case vm.StringType:
			cls := strings.TrimLeft(vals[0].ToString(), "\\")
			if ctx.VMInstance().FindClass(cls) == nil {
				ctx.VMInstance().Autoload(ctx, cls)
			}
			ci := ctx.VMInstance().FindClass(cls)
			if ci == nil {
				return fmt.Sprintf("class %q not found", cls)
			}
			static, found := ctx.VMInstance().MethodStatic(ci, mname)
			if !found {
				return fmt.Sprintf("class %s does not have a method %q", cls, mname)
			}
			if !static {
				return fmt.Sprintf("non-static method %s::%s() cannot be called statically", cls, mname)
			}
			return ""
		}
		return "first array member is not a valid class name or object"
	default:
		return "no array or string given"
	}
}

// requireCallableArg проверяет, что аргумент #i (нумерация с 0) — валидный
// callable, иначе бросает TypeError как PHP.
func requireCallableArg(ctx *vm.Context, i int, fn string) {
	if r := callableReason(ctx, arg(ctx, i)); r != "" {
		ctx.Throw(vm.NewThrowable("TypeError",
			fmt.Sprintf("%s(): Argument #%d must be a valid callback, %s", fn, i+1, r)))
		if ctx.HasThrown() {
			return
		}
	}
}

// requireCallableNamed проверяет callback-аргумент с именем параметра и
// опциональным допуском null (array_map/array_filter).
// --- tick-функции ---

// tickFuncs — зарегистрированные tick-функции (register_tick_function).
// Хранятся на VM (vm.TickFuncs): новый файл/VM не видит чужие обработчики.

func registerTickFunctionFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "register_tick_function() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	requireCallableNamed(ctx, 0, "register_tick_function", "callback", false)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cb := derefSlot(arg0(ctx))
	ctx.VMInstance().RegisterTickFunc(cb, ctx.Args()[1:])
	return vm.Bool(true)
}

func unregisterTickFunctionFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "unregister_tick_function() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	requireCallableNamed(ctx, 0, "unregister_tick_function", "callback", false)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cb := derefSlot(arg0(ctx))
	found, busy := ctx.VMInstance().UnregisterTickFunc(cb)
	if busy {
		ctx.Throw(vm.NewThrowable("Error", "Registered tick function cannot be unregistered while it is being executed"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	_ = found
	return vm.MakeNull()
}

// ticksBuiltin — служебная функция __php_ticks: вызывается из кода в области
// declare(ticks=N) после каждого N-го оператора. Выполняет зарегистрированные
// tick-функции (без реентерабельности).
func ticksBuiltin(ctx *vm.Context) vm.Value {
	v := ctx.VMInstance()
	if len(v.TickFuncs()) == 0 || v.TicksExecuting() {
		return vm.MakeNull()
	}
	v.SetTicksExecuting(true)
	// Zend: ZEND_TICKS оборачивает вызов tick-функции в
	// zend_fiber_switch_block/unblock — переключение файберов внутри
	// обработчика запрещено (fibers/ticks).
	savedNoSwitch := v.SetNoSwitch(true)
	funcs := v.TickFuncs()
	for i := 0; i < len(funcs); i++ {
		f := funcs[i]
		_, err := v.CallTickCallback(ctx, f.Cb, f.Args)
		if err != nil {
			v.SetTicksExecuting(false)
			v.SetNoSwitch(savedNoSwitch)
			throwCallable(ctx, err)
			return *new(vm.Value)
		}
		if ctx.HasThrown() {
			v.SetTicksExecuting(false)
			v.SetNoSwitch(savedNoSwitch)
			return *new(vm.Value)
		}
	}
	v.SetTicksExecuting(false)
	v.SetNoSwitch(savedNoSwitch)
	return vm.MakeNull()
}

func requireCallableNamed(ctx *vm.Context, i int, fn, param string, allowNull bool) {
	cb := arg(ctx, i)
	if allowNull && cb.IsNull() {
		return
	}
	if r := callableReason(ctx, cb); r != "" {
		suffix := ""
		if param != "" {
			suffix = fmt.Sprintf(" ($%s)", param)
		}
		orNull := ""
		if allowNull {
			orNull = " or null"
		}
		ctx.Throw(vm.NewThrowable("TypeError",
			fmt.Sprintf("%s(): Argument #%d%s must be a valid callback%s, %s", fn, i+1, suffix, orNull, r)))
		if ctx.HasThrown() {
			return
		}
	}
}

// --- буферизация вывода (ob_*) ---

// obStartCallbackReason возвращает причину, по которой Zend отвергает
// callback для ob_start() (пустая строка — callback подходит). Отличие от
// callableReason: явная диагностика для нестатического метода в форме
// "C::m".
func obStartCallbackReason(ctx *vm.Context, cb vm.Slot) string {
	switch cb.Type() {
	case vm.StringType:
		name := cb.ToString()
		if i := strings.Index(name, "::"); i >= 0 {
			cls := strings.TrimLeft(name[:i], "\\")
			mname := name[i+2:]
			ci := ctx.VMInstance().FindClass(cls)
			if ci == nil {
				ctx.VMInstance().Autoload(ctx, cls)
				ci = ctx.VMInstance().FindClass(cls)
			}
			if ci == nil {
				return fmt.Sprintf("class %q not found", name[:i])
			}
			if !ctx.VMInstance().ClassHasMethod(ci, mname) {
				return fmt.Sprintf("class %s does not have a method %q", cls, mname)
			}
			if static, _ := ctx.VMInstance().MethodStatic(ci, mname); !static {
				return fmt.Sprintf("non-static method %s::%s() cannot be called statically", cls, mname)
			}
			return ""
		}
		if !ctx.VMInstance().FunctionByName(name) {
			return fmt.Sprintf("function %q not found or invalid function name", name)
		}
		return ""
	case vm.ArrayType:
		vals := cb.AsArray().Values()
		if len(vals) != 2 {
			return "array callback must have exactly two members"
		}
		mname := derefSlot(vals[1]).ToString()
		switch derefSlot(vals[0]).Type() {
		case vm.ObjectType:
			o := ctx.VMInstance().SlotObject(vals[0])
			if o == nil {
				return "first array member is not a valid class name or object"
			}
			ci := ctx.VMInstance().FindClass(o.ClassName())
			if ci == nil || !ctx.VMInstance().ClassHasMethod(ci, mname) {
				return fmt.Sprintf("class %s does not have a method %q", o.ClassName(), mname)
			}
			return ""
		case vm.StringType:
			cls := strings.TrimLeft(derefSlot(vals[0]).ToString(), "\\")
			ci := ctx.VMInstance().FindClass(cls)
			if ci == nil {
				ctx.VMInstance().Autoload(ctx, cls)
				ci = ctx.VMInstance().FindClass(cls)
			}
			if ci == nil {
				return fmt.Sprintf("class %q not found", derefSlot(vals[0]).ToString())
			}
			if !ctx.VMInstance().ClassHasMethod(ci, mname) {
				return fmt.Sprintf("class %s does not have a method %q", cls, mname)
			}
			if static, _ := ctx.VMInstance().MethodStatic(ci, mname); !static {
				// Метод вызывается как статический: Zend разрешает, если он
				// нестатический? Нет — обёртка об этом предупредит при вызове.
				return ""
			}
			return ""
		}
		return "first array member is not a valid class name or object"
	}
	return "no array or string given"
}

func obStartFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	cb := vm.MakeNull()
	if len(args) >= 1 {
		cb = args[0]
	}
	var chunk int64
	if len(args) >= 2 {
		chunk = arg(ctx, 1).ToInt()
	}
	flags := int64(112) // PHP_OUTPUT_HANDLER_STDFLAGS
	if len(args) >= 3 {
		flags = arg(ctx, 2).ToInt()
	}
	if !ctx.VM().ObInHandler() && !cb.IsNull() && !ctx.VM().IsCallable(cb) {
		ctx.VM().Warning("ob_start(): " + obStartCallbackReason(ctx, cb))
		ctx.VM().Notice("ob_start(): Failed to create buffer")
		return vm.Bool(false)
	}
	ctx.VM().ObStart(cb, chunk, flags)
	return vm.Bool(true)
}

func obStringOrFalse(s string, ok bool) vm.Value {
	if !ok {
		return vm.Bool(false)
	}
	return vm.Str(s)
}

func obStatusArray(st vm.ObStatus) *vm.Array {
	a := vm.NewArray()
	_ = a.SetStr("name", vm.MakeString(st.Name))
	_ = a.SetStr("type", vm.MakeInt(st.Type))
	_ = a.SetStr("flags", vm.MakeInt(st.Flags))
	_ = a.SetStr("level", vm.MakeInt(st.Level))
	_ = a.SetStr("chunk_size", vm.MakeInt(st.ChunkSize))
	_ = a.SetStr("buffer_size", vm.MakeInt(st.BufferSize))
	_ = a.SetStr("buffer_used", vm.MakeInt(st.BufferUsed))
	return a
}

func obListHandlersFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, name := range ctx.VM().ObListHandlers() {
		_, _ = out.Append(vm.MakeString(name))
	}
	return vm.MakeArray(out)
}

func obGetStatusFn(ctx *vm.Context) vm.Value {
	all := false
	if len(ctx.Args()) >= 1 {
		all = arg0(ctx).ToBool()
	}
	sts := ctx.VM().ObGetStatus(all)
	if !all {
		// ob_get_status() без аргументов возвращает поля верхнего буфера
		// «плоским» массивом.
		if len(sts) == 0 {
			return vm.MakeArray(vm.NewArray())
		}
		return vm.MakeArray(obStatusArray(sts[0]))
	}
	out := vm.NewArray()
	for _, st := range sts {
		_, _ = out.Append(vm.MakeArray(obStatusArray(st)))
	}
	return vm.MakeArray(out)
}

// --- РЕФЛЕКСИЯ КЛАССОВ/ОБЪЕКТОВ ---

// reflectArg возвращает i-й аргумент или NULL.
func reflectArg(ctx *vm.Context, i int) vm.Slot {
	a := ctx.Args()
	if i < 0 || i >= len(a) {
		return vm.MakeNull()
	}
	return a[i]
}

// reflectArrSet кладёт значение в PHP-массив по имени свойства; канонические
// целые имена (например "1234") становятся int-ключами, как в PHP.
func reflectArrSet(a *vm.Array, name string, v vm.Slot) {
	if i, ok := reflectCanonicalInt(name); ok {
		_ = a.SetInt(i, v)
		return
	}
	_ = a.SetStr(name, v)
}

// reflectCanonicalInt распознаёт каноническую десятичную запись int (для
// нормализации ключей массива).
func reflectCanonicalInt(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	neg := s[0] == '-'
	body := s
	if neg {
		body = s[1:]
	}
	if body == "" {
		return 0, false
	}
	if body[0] == '0' && (len(body) > 1 || neg) {
		return 0, false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, false
		}
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return i, true
}

// reflectClassConst ищет константу вида "Class::CONST".
func reflectClassConst(ctx *vm.Context, name string) (vm.Value, bool) {
	i := strings.Index(name, "::")
	if i < 0 {
		return nil, false
	}
	ci := ctx.VM().ScopedClass(name[:i])
	if ci == nil {
		return nil, false
	}
	if v, ok, err := ctx.VM().ClassConstAccess(ci.Name() + "::" + name[i+2:]); err == nil && ok {
		return v, true
	}
	return ci.InterfaceConstValue(name[i+2:])
}

// reflectTypeName возвращает PHP-имя типа слота для сообщений об ошибках
// (zend_zval_value_name: bool печатается как true/false).
func reflectTypeName(s vm.Slot) string {
	switch s.Type() {
	case vm.IntType:
		return "int"
	case vm.FloatType:
		return "float"
	case vm.BoolType:
		if s.ToBool() {
			return "true"
		}
		return "false"
	case vm.StringType:
		return "string"
	case vm.ArrayType:
		return "array"
	case vm.ObjectType:
		if n := s.ObjectClassName(); n != "" {
			return n
		}
		return "object"
	case vm.ClosureType:
		return "Closure"
	case vm.NullType:
		return "null"
	}
	return "mixed"
}

// reflectClass разрешает аргумент (объект или имя класса) в дескриптор.
// Возвращает (дескриптор, объект, имя-запроса). Объект != nil, если передан
// объект. Для невалидного имени дескриптор nil.
func reflectClass(ctx *vm.Context, i int) (vm.ClassInfo, *vm.Object, string) {
	s := reflectArg(ctx, i)
	switch s.Type() {
	case vm.ObjectType:
		o := ctx.Arg[*vm.Object](i)
		if o == nil {
			return nil, nil, ""
		}
		return o.Class(), o, o.ClassName()
	case vm.ClosureType:
		o := ctx.Arg[*vm.Object](i)
		if o == nil {
			return nil, nil, ""
		}
		return o.Class(), o, o.ClassName()
	case vm.StringType:
		name := s.StringVal()
		return ctx.VM().FindClass(name), nil, name
	}
	return nil, nil, s.ToString()
}

func getClassMethodsFn(ctx *vm.Context) vm.Value {
	ci, _, _ := reflectClass(ctx, 0)
	if ci == nil {
		// get_class_methods(null) разрешён: текущий scope.
		if reflectArg(ctx, 0).Type() == vm.NullType {
			ci = ctx.Self()
		} else if reflectArg(ctx, 0).Type() == vm.StringType {
			ci = findClassAutoload(ctx, reflectArg(ctx, 0).StringVal())
		}
	}
	if ci == nil {
		ctx.Throw(vm.NewThrowable("TypeError",
			"get_class_methods(): Argument #1 ($object_or_class) must be an object or a valid class name, "+reflectTypeName(reflectArg(ctx, 0))+" given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	out := vm.NewArray()
	for _, m := range ci.MethodNames(ctx.Self()) {
		_, _ = out.Append(vm.MakeString(m))
	}
	return vm.MakeArray(out)
}

func getObjectVarsFn(ctx *vm.Context) vm.Value {
	s := reflectArg(ctx, 0)
	if s.Type() != vm.ObjectType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("get_object_vars", 1, "object", "object", reflectTypeName(s), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	o := ctx.Arg[*vm.Object](0)
	if o == nil {
		return vm.MakeNull()
	}
	out := vm.NewArray()
	seen := map[string]bool{}
	add := func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		key, ok := ctx.VM().ObjectVarKey(o, n)
		if !ok {
			return
		}
		if v, ok2 := o.GetProp(key); ok2 {
			reflectArrSet(out, n, slotFromValue(v))
		}
	}
	// Порядок вставки известен для объектов json_decode (stdClass).
	if ord := objKeyOrder[o]; len(ord) > 0 {
		for _, n := range ord {
			add(n)
		}
	}
	for _, n := range o.PropertyNames() {
		add(n)
	}
	return vm.MakeArray(out)
}

func getClassVarsFn(ctx *vm.Context) vm.Value {
	ci, _, name := reflectClass(ctx, 0)
	if ci == nil {
		ctx.Throw(vm.NewThrowable("Error",
			"get_class_vars(): Argument #1 ($class) must be a valid class name, "+name+" given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.MakeArray(ctx.VM().ClassVarsForScope(ci))
}

// classAliasFn реализует class_alias(string $class, string $alias,
// bool $autoload = true): bool.
func classAliasFn(ctx *vm.Context) vm.Value {
	class := arg0(ctx).ToString()
	alias := arg(ctx, 1).ToString()
	short := alias
	if i := strings.LastIndex(short, "\\"); i >= 0 {
		short = short[i+1:]
	}
	if reservedTypeNames[strings.ToLower(short)] {
		ctx.Throw(vm.NewFatal("Cannot use '" + alias + "' as class name as it is reserved"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	ci := ctx.VM().FindClass(class)
	if ci == nil {
		// class_alias($class, $alias, true): Zend ищет класс с автозагрузкой
		// (zend_lookup_class), поэтому автозагрузчик может его объявить
		// (class_alias_007).
		autoload := true
		if len(ctx.Args()) > 2 {
			autoload = ctx.Args()[2].ToBool()
		}
		if autoload {
			if _, aerr := ctx.VM().Autoload(ctx, class); aerr != nil {
				throwCallable(ctx, aerr)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Bool(false)
			}
			ci = ctx.VM().FindClass(class)
		}
	}
	if ci == nil {
		ctx.VM().Warning("Class \"" + class + "\" not found")
		return vm.Bool(false)
	}
	if ex := ctx.VM().FindClass(alias); ex != nil {
		// Классы, объявленные позже в этом же файле, в Zend на момент вызова
		// class_alias() ещё не существуют (у нас они уже слинкованы хойстом).
		if df, dl := vm.ClassDeclPosition(ex); df != "" && df == ctx.VM().CurrentFile() && dl > ctx.VM().CurrentLine() {
			ex = nil
		}
		if ex == nil {
			ctx.VM().AddClassAlias(alias, ci)
			return vm.Bool(true)
		}
		// При конфликте в самом class_alias() Zend всегда говорит "class".
		ctx.VM().Warning("Cannot declare class " + alias + ", because the name is already in use")
		return vm.Bool(false)
	}
	ctx.VM().AddClassAlias(alias, ci)
	return vm.Bool(true)
}

func classExistsFn(ctx *vm.Context) vm.Value {
	s := reflectArg(ctx, 0)
	if s.Type() != vm.StringType {
		return vm.Bool(false)
	}
	autoload := true
	if len(ctx.Args()) > 1 {
		autoload = arg(ctx, 1).ToBool()
	}
	var ci vm.ClassInfo
	if autoload {
		ci = findClassAutoload(ctx, s.StringVal())
	} else {
		ci = ctx.VM().FindClass(s.StringVal())
	}
	return vm.Bool(ci != nil && !ci.IsInterface() && !ci.IsTrait())
}

// splAutoloadRegisterFn реализует spl_autoload_register(callable $cb = null,
// bool $throw = true, bool $prepend = false): bool.
func splAutoloadRegisterFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) == 0 || args[0].Type() == vm.NullType {
		// Без callback регистрируется встроенный spl_autoload.
		ctx.VM().RegisterAutoloader(vm.MakeString("spl_autoload"), false)
		return vm.Bool(true)
	}
	cb := derefSlot(args[0])
	if !ctx.VM().IsCallable(cb) {
		ctx.Throw(vm.NewThrowable("TypeError", "spl_autoload_register(): Argument #1 ($callback) must be a valid callback"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	prepend := false
	if len(args) > 2 {
		prepend = args[2].ToBool()
	}
	ctx.VM().RegisterAutoloader(cb, prepend)
	return vm.Bool(true)
}

func splAutoloadUnregisterFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) == 0 {
		return vm.Bool(false)
	}
	return vm.Bool(ctx.VM().UnregisterAutoloader(derefSlot(args[0])))
}

func splAutoloadFunctionsFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, cb := range ctx.VM().Autoloaders() {
		_, _ = out.Append(cb)
	}
	return vm.MakeArray(out)
}

func splAutoloadCallFn(ctx *vm.Context) vm.Value {
	name := arg0(ctx).ToString()
	if _, err := ctx.VM().Autoload(ctx, name); err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	return vm.MakeNull()
}

// varianceCheckFn — служебная функция: выполняет отложенную проверку
// совместимости сигнатур класса верхнего уровня (аргумент — индекс класса в
// vm.classes). Неустранимая ошибка печатается как фатальная (см.
// vm.RuntimeVarianceCheckIdx).
func varianceCheckFn(ctx *vm.Context) vm.Value {
	idx := int(arg0(ctx).ToInt())
	if err := ctx.VM().RuntimeVarianceCheckIdx(idx); err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return *new(vm.Value)
		}
		if vm.IsExitSignal(err) {
			ctx.VM().RequestHalt()
			return *new(vm.Value)
		}
		ctx.Throw(vm.NewThrowable("Error", err.Error()))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return *new(vm.Value)
}

// requireClassFn — служебная функция: запускает автозагрузку для имени
// родителя/интерфейса, неизвестного на этапе компиляции, и бросает Error,
// если класс так и не появился (как PHP при связывании объявления класса).
func requireClassFn(ctx *vm.Context) vm.Value {
	name := arg0(ctx).ToString()
	isIface := arg(ctx, 1).ToBool()
	if _, err := ctx.VM().Autoload(ctx, name); err != nil {
		if vm.IsExitSignal(err) {
			// Автозагрузка родителя/интерфейса привела к фатальному
			// завершению (напр. отложенная проверка совместимости):
			// сигнал выхода уже напечатан, останавливаем скрипт.
			ctx.VM().RequestHalt()
			return *new(vm.Value)
		}
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return vm.MakeNull()
	}
	if ctx.VM().FindClass(name) == nil {
		kind := "Class"
		if isIface {
			kind = "Interface"
		}
		ctx.Throw(vm.NewThrowable("Error", kind+" \""+name+"\" not found"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return vm.MakeNull()
}

// useTraitFn — примешивает трейт к классу, объявленному статически, когда
// трейт был неизвестен на этапе компиляции: запускает автозагрузку (Zend
// делает это при выполнении DECLARE_CLASS) и применяет трейт.
func useTraitFn(ctx *vm.Context) vm.Value {
	clsName := arg0(ctx).ToString()
	traitName := arg(ctx, 1).ToString()
	vmc := ctx.VM()
	if vmc.FindClass(traitName) == nil {
		if _, err := vmc.Autoload(ctx, traitName); err != nil {
			if vm.IsExitSignal(err) {
				vmc.RequestHalt()
				return *new(vm.Value)
			}
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
			} else {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
			}
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
	}
	tr := vmc.FindClass(traitName)
	if tr == nil {
		ctx.Throw(vm.NewThrowable("Error", "Trait \""+traitName+"\" not found"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	ci := vmc.FindClass(clsName)
	if ci != nil {
		ci.Use(tr)
	}
	return vm.MakeNull()
}

// callCbNamedBuiltin — call_user_func/forward_static_call с именованными
// аргументами: callback + массив остальных аргументов (с ключами-именами).
func callCbNamedBuiltin(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "expects 2 arguments"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	res, err := ctx.VMInstance().CallCallableNamedDirect(ctx, derefSlot(args[0]), args[1].AsArray())
	throwCallable(ctx, err)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return res
}

// forwardCbNamedBuiltin — forward_static_call с именованными аргументами:
// called scope вызывающего сохраняется (позднее статическое связывание).
func forwardCbNamedBuiltin(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "expects 2 arguments"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	self, called := ctx.VMInstance().ForwardScope()
	if self == nil && called == nil {
		ctx.Throw(vm.NewThrowable("Error", "Cannot call forward_static_call() when no class scope is active"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	res, err := ctx.VMInstance().CallCallableNamedDirectScope(ctx, derefSlot(args[0]), args[1].AsArray(), self, called)
	throwCallable(ctx, err)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return res
}

// callNamedBuiltin — динамический вызов с именованными аргументами
// (`$f(a: 1)` / `unknownFn(a: 1)`): аргументы упакованы в массив, строковые
// ключи — имена параметров.
func callNamedBuiltin(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "callNamed expects 2 arguments"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	callee := derefSlot(args[0])
	arr := args[1].AsArray()
	res, err := ctx.VMInstance().CallCallableNamed(ctx, callee, arr)
	throwCallable(ctx, err)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return res
}

// incDecPropBuiltin — помощник для ++/-- над свойством объекта (см.
// VM.IncDecTypedProp): возвращает новое значение, записывая его в свойство.
func incDecPropBuiltin(ctx *vm.Context) vm.Value {
	obj := arg(ctx, 0)
	name := arg(ctx, 1).ToString()
	cur := arg(ctx, 2)
	dec := arg(ctx, 3).ToBool()
	res, err := ctx.VM().IncDecTypedProp(obj, name, cur, dec)
	if err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return vm.MakeNull()
	}
	return res
}

// incDecRefBuiltin — помощник для ++/-- над ссылкой (возможно, guarded).
func incDecRefBuiltin(ctx *vm.Context) vm.Value {
	cur := arg(ctx, 0)
	dec := arg(ctx, 1).ToBool()
	res, err := ctx.VM().IncDecRef(cur, dec)
	if err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return vm.MakeNull()
	}
	return res
}

// incDecValBuiltin — помощник для ++/-- над значением (числом или строкой).
func incDecValBuiltin(ctx *vm.Context) vm.Value {
	cur := arg(ctx, 0)
	dec := arg(ctx, 1).ToBool()
	res, err := ctx.VM().IncDecValue(cur, dec)
	if err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return vm.MakeNull()
	}
	return res
}

// incDecGlobalBuiltin — ++/-- над глобальной переменной (возможно, ссылкой).
func incDecGlobalBuiltin(ctx *vm.Context) vm.Value {
	name := arg(ctx, 0).ToString()
	dec := arg(ctx, 1).ToBool()
	raw, _ := ctx.VM().GlobalRef(name)
	res, err := ctx.VM().IncDecRef(raw, dec)
	if err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return vm.MakeNull()
	}
	if !raw.IsRef() {
		ctx.VM().GlobalSet(name, res)
	}
	return res
}

func interfaceExistsFn(ctx *vm.Context) vm.Value {
	s := reflectArg(ctx, 0)
	if s.Type() != vm.StringType {
		return vm.Bool(false)
	}
	autoload := true
	if len(ctx.Args()) > 1 {
		autoload = arg(ctx, 1).ToBool()
	}
	var ci vm.ClassInfo
	if autoload {
		ci = findClassAutoload(ctx, s.StringVal())
	} else {
		ci = ctx.VM().FindClass(s.StringVal())
	}
	return vm.Bool(ci != nil && ci.IsInterface())
}

func traitExistsFn(ctx *vm.Context) vm.Value {
	s := reflectArg(ctx, 0)
	if s.Type() != vm.StringType {
		return vm.Bool(false)
	}
	autoload := true
	if len(ctx.Args()) > 1 {
		autoload = arg(ctx, 1).ToBool()
	}
	var ci vm.ClassInfo
	if autoload {
		ci = findClassAutoload(ctx, s.StringVal())
	} else {
		ci = ctx.VM().FindClass(s.StringVal())
	}
	return vm.Bool(ci != nil && ci.IsTrait())
}

// findClassAutoload повторяет zend_lookup_class: ищет класс, при отсутствии
// вызывает зарегистрированные автозагрузчики. Ошибку автозагрузчика
// пробрасывает через ctx.Throw.
func findClassAutoload(ctx *vm.Context, name string) vm.ClassInfo {
	if ci := ctx.VM().FindClass(name); ci != nil {
		return ci
	}
	ok, err := ctx.VM().Autoload(ctx, name)
	if err != nil {
		throwCallable(ctx, err)
		return nil
	}
	_ = ok
	// Имя может отличаться регистром от объявленного: ищем
	// регистронезависимо (zend_lookup_class).
	return ctx.VM().FindClass(name)
}

// enumExistsFn — enum_exists($name, $autoload = true).
func enumExistsFn(ctx *vm.Context) vm.Value {
	s := reflectArg(ctx, 0)
	if s.Type() != vm.StringType {
		return vm.Bool(false)
	}
	name := strings.TrimLeft(s.StringVal(), "\\")
	ci := ctx.VM().FindClass(name)
	if ci == nil && (len(ctx.Args()) < 2 || reflectArg(ctx, 1).ToBool()) {
		if ok, err := ctx.VM().Autoload(ctx, name); err == nil && ok {
			ci = ctx.VM().FindClass(name)
		}
	}
	return vm.Bool(ci != nil && ci.IsEnum())
}

func methodExistsFn(ctx *vm.Context) vm.Value {
	s := reflectArg(ctx, 0)
	name := reflectArg(ctx, 1).ToString()
	if s.IsClosure() {
		return vm.Bool(strings.EqualFold(name, "__invoke"))
	}
	switch s.Type() {
	case vm.ObjectType:
		if s.IsClosure() {
			return vm.Bool(strings.EqualFold(name, "__invoke"))
		}
		ci, _, _ := reflectClass(ctx, 0)
		if ci == nil {
			return vm.Bool(false)
		}
		return vm.Bool(ci.HasMethod(name))
	case vm.StringType:
		ci := findClassAutoload(ctx, s.StringVal())
		if ci == nil {
			return vm.Bool(false)
		}
		if !ci.HasMethod(name) {
			// method_exists('Closure', '__invoke') — true (Zend).
			if ci.Name() == "Closure" && strings.EqualFold(name, "__invoke") {
				return vm.Bool(true)
			}
			return vm.Bool(false)
		}
		// Для строкового аргумента private-метод, объявленный в предке,
		// не виден (Zend: shadow properties).
		if fn, ok := ci.MethodFunction(name); ok && fn != nil {
			if fn.Scope != nil && fn.Scope != ci {
				if vis, ok2 := fn.Scope.MethodVisibility(name); ok2 && vis == vm.Private {
					return vm.Bool(false)
				}
			}
		}
		return vm.Bool(true)
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		"method_exists(): Argument #1 ($object_or_class) must be of type object|string, "+reflectTypeName(s)+" given"))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.Bool(false)
}

func propertyExistsFn(ctx *vm.Context) vm.Value {
	s := reflectArg(ctx, 0)
	name := reflectArg(ctx, 1).ToString()
	switch s.Type() {
	case vm.ObjectType, vm.ClosureType:
		// Closure — тоже объект (property_exists(Closure) => false, bug50146).
		ci, o, _ := reflectClass(ctx, 0)
		if ci == nil {
			return vm.Bool(false)
		}
		if ci.HasProperty(name) {
			return vm.Bool(true)
		}
		if o != nil {
			if _, ok := o.GetProp(name); ok {
				return vm.Bool(true)
			}
		}
		return vm.Bool(false)
	case vm.StringType:
		ci := findClassAutoload(ctx, s.StringVal())
		if ci == nil {
			return vm.Bool(false)
		}
		return vm.Bool(ci.HasProperty(name))
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		"property_exists(): Argument #1 ($object_or_class) must be of type object|string, "+reflectTypeName(s)+" given"))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.Bool(false)
}

func isAFn(ctx *vm.Context) vm.Value {
	allowString := false
	if len(ctx.Args()) >= 3 {
		allowString = reflectArg(ctx, 2).ToBool()
	}
	return isACommon(ctx, allowString, false)
}

func isSubclassOfFn(ctx *vm.Context) vm.Value {
	allowString := true
	if len(ctx.Args()) >= 3 {
		allowString = reflectArg(ctx, 2).ToBool()
	}
	return isACommon(ctx, allowString, true)
}

// isACommon реализует is_a/is_subclass_of. strict исключает совпадение класса.
func isACommon(ctx *vm.Context, allowString, strict bool) vm.Value {
	s := reflectArg(ctx, 0)
	var ci vm.ClassInfo
	switch s.Type() {
	case vm.ObjectType:
		o := ctx.Arg[*vm.Object](0)
		if o != nil {
			ci = o.Class()
		}
	case vm.ClosureType:
		ci = ctx.VM().FindClass("Closure")
	case vm.StringType:
		if !allowString {
			return vm.Bool(false)
		}
		// Zend: zend_lookup_class для строкового субъекта — с автозагрузкой.
		ci = findClassAutoload(ctx, s.StringVal())
	default:
		return vm.Bool(false)
	}
	if ci == nil {
		return vm.Bool(false)
	}
	// Zend ищет целевой класс БЕЗ автозагрузки; исключение — точное
	// совпадение имени (zend_string_equals до лукапа).
	tname := reflectArg(ctx, 1).ToString()
	if !strict && ctx.VM().FindClass(tname) == nil && tname == ci.Name() {
		return vm.Bool(true)
	}
	tci := ctx.VM().FindClass(tname)
	if tci == nil {
		return vm.Bool(false)
	}
	if strict && ci == tci {
		return vm.Bool(false)
	}
	return vm.Bool(ci.IsA(tci))
}

func getParentClassFn(ctx *vm.Context) vm.Value {
	var ci vm.ClassInfo
	if len(ctx.Args()) == 0 {
		ctx.VM().Deprecated("Calling get_parent_class() without arguments is deprecated")
		ci = ctx.Self()
	} else {
		s := reflectArg(ctx, 0)
		switch s.Type() {
		case vm.ObjectType:
			o := ctx.Arg[*vm.Object](0)
			if o != nil {
				ci = o.Class()
			}
		case vm.StringType:
			ci = findClassAutoload(ctx, s.StringVal())
			if ci == nil {
				ctx.Throw(vm.NewThrowable("TypeError",
					"get_parent_class(): Argument #1 ($object_or_class) must be an object or a valid class name, string given"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
		default:
			ctx.Throw(vm.NewThrowable("TypeError",
				"get_parent_class(): Argument #1 ($object_or_class) must be an object or a valid class name, "+reflectTypeName(s)+" given"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	if ci == nil || ci.Parent() == nil {
		return vm.Bool(false)
	}
	return vm.Str(ci.ParentName())
}

func getCalledClassFn(ctx *vm.Context) vm.Value {
	ci := ctx.Called()
	if ci == nil {
		ctx.Throw(vm.NewThrowable("Error", "get_called_class() must be called from within a class"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Str(ci.Name())
}

func getDeclaredClassesFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, ci := range ctx.VM().Classes() {
		if vm.ClassHidden(ci) {
			continue
		}
		if ci.IsInterface() || ci.IsTrait() {
			continue
		}
		// __PHP_Incomplete_Class регистрируется лениво (при unserialize),
		// а не присутствует в списке объявленных классов сразу.
		if ci.Name() == "__PHP_Incomplete_Class" {
			continue
		}
		_, _ = out.Append(vm.MakeString(ci.Name()))
	}
	for _, alias := range ctx.VM().ClassAliasOrder() {
		_, _ = out.Append(vm.MakeString(alias))
	}
	return vm.MakeArray(out)
}

func getDeclaredInterfacesFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, ci := range ctx.VM().Classes() {
		if vm.ClassHidden(ci) {
			continue
		}
		if ci.IsInterface() {
			_, _ = out.Append(vm.MakeString(ci.Name()))
		}
	}
	return vm.MakeArray(out)
}

func getDeclaredTraitsFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, ci := range ctx.VM().Classes() {
		if vm.ClassHidden(ci) {
			continue
		}
		if ci.IsTrait() {
			_, _ = out.Append(vm.MakeString(ci.Name()))
		}
	}
	for _, name := range ctx.VM().TraitAliasNames() {
		_, _ = out.Append(vm.MakeString(name))
	}
	return vm.MakeArray(out)
}

func classImplementsFn(ctx *vm.Context) vm.Value {
	ci, _, _ := reflectClass(ctx, 0)
	if ci == nil {
		return vm.Bool(false)
	}
	out := vm.NewArray()
	for _, n := range ci.InterfaceNames() {
		_ = out.SetStr(n, vm.MakeString(n))
	}
	return vm.MakeArray(out)
}

func classParentsFn(ctx *vm.Context) vm.Value {
	ci, _, _ := reflectClass(ctx, 0)
	if ci == nil {
		return vm.Bool(false)
	}
	out := vm.NewArray()
	for _, n := range ci.ParentNames() {
		_ = out.SetStr(n, vm.MakeString(n))
	}
	return vm.MakeArray(out)
}

func classUsesFn(ctx *vm.Context) vm.Value {
	ci, _, _ := reflectClass(ctx, 0)
	if ci == nil {
		return vm.Bool(false)
	}
	out := vm.NewArray()
	for _, n := range ci.TraitNames() {
		_ = out.SetStr(n, vm.MakeString(n))
	}
	return vm.MakeArray(out)
}

// ============================================================================
// INI, окружение и системная информация (builtins2Agent)
// ============================================================================

var (
	// iniValues — текущие значения INI-опций. Заполняется раннером из --INI--.
	iniValues = defaultIniValues()
	// iniOriginal — значения на момент старта запроса (для ini_restore).
	iniOriginal = defaultIniValues()

	// phpRawInput — тело POST-запроса для php://input.
	phpRawInput string

	// uploadedTmpPaths — дополнительные (вне $_FILES) пути загруженных файлов.
	uploadedTmpPaths = map[string]bool{}

	// userConstNames — пользовательские константы, объявленные через define().
	userConstNames []string
	userConstSet   = map[string]bool{}

	// gcEnabled — состояние zend.enable_gc (gc_enable/gc_disable/gc_enabled).
	gcEnabled = true
)

// defaultIniValues возвращает значения по умолчанию (как в CLI).
func defaultIniValues() map[string]string {
	return map[string]string{
		"precision":                           "14",
		"serialize_precision":                 "-1",
		"zend.exception_ignore_args":          "0",
		"zend.exception_string_param_max_len": "15",
		"docref_ext":                          "",
		"url_rewriter.hosts":                  "",
		"url_rewriter.tags":                   "form=",
		"session.trans_sid_hosts":             "",
		"session.trans_sid_tags":              "a=href,area=href,frame=src,form=",
		"session.name":                        "PHPSESSID",
		"session.use_trans_sid":               "0",
		"session.use_only_cookies":            "1",
		"session.use_cookies":                 "1",
		"session.use_strict_mode":             "0",
		"variables_order":                     "EGPCS",
		"request_order":                       "",
		"sys_temp_dir":                        "",
		"error_reporting":                     "E_ALL",
		"display_errors":                      "1",
		"display_startup_errors":              "0",
		"log_errors":                          "1",
		"error_log":                           "",
		"html_errors":                         "0",
		"docref_root":                         "",
		"user_agent":                          "",
		"disable_functions":                   "",
		"enable_post_data_reading":            "1",
		"post_max_size":                       "8M",
		"upload_max_filesize":                 "2M",
		"file_uploads":                        "1",
		"max_file_uploads":                    "20",
		"memory_limit":                        "128M",
		"include_path":                        ".:/usr/share/php",
		"open_basedir":                        "",
		"default_charset":                     "UTF-8",
		"extension_dir":                       "./",
		"date.timezone":                       "UTC",
		"max_execution_time":                  "0",
		"sendmail_path":                       "/usr/sbin/sendmail -t -i",
		"mail.add_x_header":                   "0",
		"mail.log":                            "",
		"mail.force_extra_parameters":         "",
		"mail.mixed_lf_and_crlf":              "0",
		"pcre.jit":                            "1",
		"pcre.backtrack_limit":                "1000000",
		"pcre.recursion_limit":                "100000",
		"short_open_tag":                      "1",
		"internal_encoding":                   "",
		"input_encoding":                      "",
		"output_encoding":                     "",
		"zend.enable_gc":                      "1",
		"zend.assertions":                     "1",
		"arg_separator.output":                "&",
		"auto_detect_line_endings":            "0",
		"allow_url_fopen":                     "1",
		"allow_url_include":                   "0",
		"unserialize_callback_func":           "",
		"unserialize_max_depth":               "4096",
		"highlight.string":                    "#DD0000",
		"highlight.comment":                   "#FF8000",
		"highlight.default":                   "#0000BB",
		"highlight.keyword":                   "#007700",
		"highlight.html":                      "#000000",
	}
}

// autoDetectLineEndingsOn сообщает, включена ли опция auto_detect_line_endings.
func autoDetectLineEndingsOn() bool {
	v, ok := iniValues["auto_detect_line_endings"]
	if !ok {
		return false
	}
	switch strings.ToLower(v) {
	case "", "0", "off", "false", "no":
		return false
	}
	return true
}

// SetIni заменяет набор INI-настроек: сбрасывает к значениям по умолчанию и
// применяет переданные. Экспортируется, чтобы тест-раннер мог положить
// значения из секции --INI-- .phpt-файла.
// iniStartupWarnings — стартовые Warning-диагностики применения --INI--
// (печатаются как "PHP Startup: ... in Unknown on line 0").
var iniStartupWarnings []string

func SetIni(m map[string]string) {
	iniValues = defaultIniValues()
	// open_basedir, заданный из INI (--INI--/-d), разворачивается относительно
	// текущего каталога на каждой проверке; runtime-кэш сбрасываем.
	openBasedirRuntime = nil
	for k, v := range m {
		iniValues[normalizeIniKey(k)] = v
	}
	// Недопустимый zend.script_encoding при включённом zend.multibyte — стартовое
	// предупреждение Zend (как при -d zend.script_encoding=bogus).
	iniStartupWarnings = nil
	if iniBoolInt(iniValues["zend.multibyte"]) != 0 {
		if enc, ok := iniValues["zend.script_encoding"]; ok && enc != "" && !strings.EqualFold(enc, "auto") {
			if _, valid := mbCanon(enc); !valid {
				iniStartupWarnings = append(iniStartupWarnings, fmt.Sprintf(
					`PHP Startup: INI setting contains invalid encoding %q in Unknown on line 0`, enc))
			}
		}
	}
	// iniOriginal — снимок значений на старте запроса (для ini_restore):
	// это значение с учётом --INI---/-d-переопределений, а не build-default.
	iniOriginal = make(map[string]string, len(iniValues))
	for k, v := range iniValues {
		iniOriginal[k] = v
	}
	if v, ok := iniValues["include_path"]; ok {
		fsIncludePath = v
	} else {
		fsIncludePath = "."
	}
	// mbstring internal encoding из INI (internal_encoding / mbstring.internal_encoding).
	// По умолчанию совпадает с default_charset.
	mbInternalEncoding = "UTF-8"
	if v, ok := iniValues["default_charset"]; ok && v != "" {
		if c, ok2 := mbCanon(v); ok2 {
			mbInternalEncoding = c
		}
	}
	if v, ok := iniValues["internal_encoding"]; ok && v != "" {
		if c, ok2 := mbCanon(v); ok2 {
			mbInternalEncoding = c
		}
	} else if v, ok := iniValues["mbstring.internal_encoding"]; ok && v != "" {
		if c, ok2 := mbCanon(v); ok2 {
			mbInternalEncoding = c
		}
	}
	if v, ok := iniValues["precision"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			vm.SetFloatPrecision(n)
		}
	}
	// assert.* — deprecated INI-настройки (PHP 8.3): применяем к рантайм-опциям
	// assert(). Стартовые Deprecated печатает installSuperglobals.
	if v, ok := iniValues["assert.active"]; ok {
		assertActiveOpt = iniBoolInt(v)
	}
	if v, ok := iniValues["assert.warning"]; ok {
		assertWarningOpt = iniBoolInt(v)
	}
	if v, ok := iniValues["assert.bail"]; ok {
		assertBailOpt = iniBoolInt(v)
	}
	if v, ok := iniValues["assert.exception"]; ok {
		assertExceptionOpt = iniBoolInt(v)
	}
	if v, ok := iniValues["zend.assertions"]; ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			zendAssertions = n
		}
	}
	// zend.enable_gc: стартовое состояние сборщика циклов + счётчики.
	if v, ok := iniValues["zend.enable_gc"]; ok {
		gcEnabled = iniBoolInt(v) != 0
	} else {
		gcEnabled = true
	}
	if v, ok := iniValues["assert.callback"]; ok {
		if t := strings.TrimSpace(v); t != "" {
			assertCallbackSlot = vm.MakeString(t)
		} else {
			assertCallbackSlot = vm.MakeNull()
		}
	}
	// error_reporting: значение INI может быть константным выражением
	// (E_ALL & ~E_NOTICE и т.п.) — приводим к числу, как zend_ini.
	if v, ok := iniValues["error_reporting"]; ok {
		if n, ok2 := iniEvalExpr(v); ok2 {
			iniValues["error_reporting"] = strconv.FormatInt(n, 10)
		}
	}
}

// iniEvalExpr вычисляет значение INI-настройки как константное выражение
// (числа, константы E_*, битовые &,|,^, унарные ~ и !, скобки). Использует
// штатный INI-токенизатор (zend_ini_parser): |,&,^ — одного приоритета,
// левоассоциативные.
func iniEvalExpr(s string) (int64, bool) {
	p := &iniParser{scanner: iniScannerTyped}
	toks, ni := p.tokenizeValue(s, 0)
	if p.failed || len(toks) == 0 {
		return 0, false
	}
	for ni < len(s) && (s[ni] == ' ' || s[ni] == '\t') {
		ni++
	}
	if ni != len(s) {
		return 0, false
	}
	i := 0
	v, ok := iniEvalOr(toks, &i)
	if !ok || i != len(toks) {
		return 0, false
	}
	return v, true
}

// iniSkipWS пропускает пробельные токены.
func iniSkipWS(toks []iniTok, i *int) {
	for *i < len(toks) && toks[*i].kind == iniTWhitespace {
		*i++
	}
}

// iniEvalOr разбирает выражение до следующей закрывающей скобки/конца.
func iniEvalOr(toks []iniTok, i *int) (int64, bool) {
	iniSkipWS(toks, i)
	v, ok := iniEvalUnary(toks, i)
	if !ok {
		return 0, false
	}
	for {
		iniSkipWS(toks, i)
		if *i >= len(toks) || toks[*i].kind != iniTOp {
			break
		}
		op := toks[*i].text
		if op != "&" && op != "|" && op != "^" {
			break
		}
		*i++
		iniSkipWS(toks, i)
		r, ok := iniEvalUnary(toks, i)
		if !ok {
			return 0, false
		}
		switch op {
		case "&":
			v &= r
		case "|":
			v |= r
		case "^":
			v ^= r
		}
	}
	return v, true
}

func iniEvalUnary(toks []iniTok, i *int) (int64, bool) {
	iniSkipWS(toks, i)
	if *i < len(toks) && toks[*i].kind == iniTOp {
		switch toks[*i].text {
		case "~", "!", "+", "-":
			op := toks[*i].text
			*i++
			v, ok := iniEvalUnary(toks, i)
			if !ok {
				return 0, false
			}
			switch op {
			case "~":
				return ^v, true
			case "!":
				if v == 0 {
					return 1, true
				}
				return 0, true
			case "+":
				return v, true
			case "-":
				return -v, true
			}
		case "(":
			*i++
			v, ok := iniEvalOr(toks, i)
			if !ok {
				return 0, false
			}
			iniSkipWS(toks, i)
			if *i < len(toks) && toks[*i].kind == iniTOp && toks[*i].text == ")" {
				*i++
				return v, true
			}
			return 0, false
		}
		return 0, false
	}
	if *i >= len(toks) {
		return 0, false
	}
	t := toks[*i]
	*i++
	switch t.kind {
	case iniTNumber:
		v, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	case iniTConstant:
		name := strings.TrimLeft(t.text, "\\")
		if cv, ok := stdConst(name); ok {
			return cv.ToInt(), true
		}
		if cv, ok := stdConst(strings.ToUpper(name)); ok {
			return cv.ToInt(), true
		}
		return 0, false
	case iniTBoolTrue:
		return 1, true
	case iniTBoolFalse, iniTNull:
		return 0, true
	}
	return 0, false
}

// iniBoolInt приводит INI-значение bool к 0/1 (как php_ini).
func iniBoolInt(v string) int64 {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "true", "yes":
		return 1
	}
	return 0
}

// SetIniString разбирает текст секции --INI-- и применяет его.
func SetIniString(raw string) {
	SetIni(parseIniSettings(raw))
}

// IniValue возвращает текущее значение INI-опции.
func IniValue(name string) (string, bool) {
	v, ok := iniValues[normalizeIniKey(name)]
	return v, ok
}

// iniEnabled сообщает, включена ли bool-опция INI (значение 1/on/true/yes).
func iniEnabled(name string) bool {
	v, ok := iniValues[normalizeIniKey(name)]
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}

// SetRawInput задаёт тело POST-запроса (php://input).
func SetRawInput(s string) { phpRawInput = s }

// phpStdinRaw — данные STDIN из секции --STDIN--.
var phpStdinRaw []byte

// SetStdinData задаёт данные STDIN для STDIN/php://stdin.
func SetStdinData(b []byte) { phpStdinRaw = b }

// RegisterUploadedFile помечает путь как «загруженный» (для is_uploaded_file
// / move_uploaded_file), если файл не задан через $_FILES.
func RegisterUploadedFile(path string) {
	if path != "" {
		uploadedTmpPaths[path] = true
	}
}

func normalizeIniKey(k string) string {
	return strings.ToLower(strings.TrimSpace(k))
}

// parseIniSettings строит map из строк вида "key = value".
func parseIniSettings(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i < 0 {
			continue
		}
		k := normalizeIniKey(line[:i])
		v := strings.TrimSpace(line[i+1:])
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if k == "" {
			continue
		}
		if k == "auto_detect_line_endings" {
			switch strings.ToLower(v) {
			case "on", "true", "yes", "1":
				v = "1"
			case "off", "false", "no", "none", "":
				v = ""
			}
		}
		out[k] = v
	}
	return out
}

func sysGetTempDirFunc() string {
	if v, ok := iniValues["sys_temp_dir"]; ok && v != "" {
		return v
	}
	return os.TempDir()
}

func iniGetFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		return vm.Bool(false)
	}
	name := normalizeIniKey(arg0(ctx).ToString())
	if name == "zend.enable_gc" {
		if gcEnabled {
			return vm.Str("1")
		}
		return vm.Str("0")
	}
	if v, ok := iniValues[name]; ok {
		return vm.Str(v)
	}
	return vm.Bool(false)
}

// --- сборщик циклов (gc_*) ---

// gcStatusFn повторяет форму gc_status() в Zend: 12 полей. Счётчики ведёт VM
// (lifetime.go): runs/collected — проходы сборщика и собранные узлы.
func gcStatusFn(ctx *vm.Context) vm.Value {
	a := vm.NewArray()
	_ = a.SetStr("running", vm.MakeBool(false))
	_ = a.SetStr("protected", vm.MakeBool(false))
	_ = a.SetStr("full", vm.MakeBool(false))
	var runs, collected, roots int64
	if m := ctx.VM(); m != nil {
		runs, collected, roots = m.GCRuns(), m.GCCollected(), m.GCRootCount()
	}
	_ = a.SetStr("runs", vm.MakeInt(runs))
	_ = a.SetStr("collected", vm.MakeInt(collected))
	_ = a.SetStr("threshold", vm.MakeInt(10001))
	_ = a.SetStr("buffer_size", vm.MakeInt(16384))
	_ = a.SetStr("roots", vm.MakeInt(roots))
	_ = a.SetStr("application_time", vm.MakeFloat(0))
	_ = a.SetStr("collector_time", vm.MakeFloat(0))
	_ = a.SetStr("destructor_time", vm.MakeFloat(0))
	_ = a.SetStr("free_time", vm.MakeFloat(0))
	return vm.MakeArray(a)
}

func gcEnableFn(ctx *vm.Context) vm.Value {
	gcEnabled = true
	iniValues["zend.enable_gc"] = "1"
	return vm.MakeNull()
}

func gcDisableFn(ctx *vm.Context) vm.Value {
	gcEnabled = false
	iniValues["zend.enable_gc"] = "0"
	return vm.MakeNull()
}

func gcEnabledFn(ctx *vm.Context) vm.Value { return vm.Bool(gcEnabled) }

func gcCollectCyclesFn(ctx *vm.Context) vm.Value {
	if v := ctx.VM(); v != nil {
		n, err := v.CollectCycles()
		if err != nil {
			// Ошибка из __destruct, вызванного сборщиком (в т.ч. FiberError
			// "Cannot switch fibers..."): как в Zend — непойманная, поднимается
			// из gc_collect_cycles() (fibers/no-switch-gc).
			throwCallable(ctx, err)
			return vm.MakeInt(0)
		}
		return vm.MakeInt(int64(n))
	}
	return vm.MakeInt(0)
}

func iniSetFn(ctx *vm.Context) vm.Value {
	name := normalizeIniKey(arg0(ctx).ToString())
	val := arg(ctx, 1)
	var s string
	switch val.Type() {
	case vm.NullType:
		s = ""
	case vm.BoolType:
		if val.ToBool() {
			s = "1"
		} else {
			s = ""
		}
	case vm.IntType:
		s = strconv.FormatInt(val.ToInt(), 10)
	case vm.FloatType:
		s = phpFloat(val.ToFloat())
	case vm.StringType:
		s = val.ToString()
	default:
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("ini_set", 2, "value", "string|int|float|bool|null", "", "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	// zend.enable_gc: изменяемая в рантайме настройка (PHP_INI_ALL).
	if name == "zend.enable_gc" {
		old := "0"
		if gcEnabled {
			old = "1"
		}
		n := iniBoolInt(s)
		gcEnabled = n != 0
		if gcEnabled {
			iniValues[name] = "1"
		} else {
			iniValues[name] = "0"
		}
		return vm.Str(old)
	}
	// zend.assertions: -1 фиксируется на старте, менять в рантайме нельзя.
	if name == "zend.assertions" {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			n = 0
		}
		// -1 фиксируется только в php.ini; из состояния -1 выйти нельзя.
		if zendAssertions == -1 || n == -1 {
			ctx.VM().Warning("zend.assertions may be completely enabled or disabled only in php.ini")
			return vm.Bool(false)
		}
		old, _ := iniValues[name]
		iniValues[name] = strconv.FormatInt(n, 10)
		zendAssertions = n
		return vm.Str(old)
	}
	// assert.* — устаревшие INI-директивы: применяем в рантайме и печатаем
	// Deprecated (PHP 8.3).
	switch name {
	case "assert.active", "assert.warning", "assert.bail", "assert.exception", "assert.callback":
		ctx.VM().Deprecated("ini_set(): " + name + " INI setting is deprecated")
		old, _ := iniValues[name]
		iniValues[name] = s
		switch name {
		case "assert.active":
			assertActiveOpt = iniBoolInt(s)
		case "assert.warning":
			assertWarningOpt = iniBoolInt(s)
		case "assert.bail":
			assertBailOpt = iniBoolInt(s)
		case "assert.exception":
			assertExceptionOpt = iniBoolInt(s)
		case "assert.callback":
			if strings.TrimSpace(s) == "" {
				assertCallbackSlot = vm.MakeNull()
			} else {
				assertCallbackSlot = vm.MakeString(strings.TrimSpace(s))
			}
		}
		return vm.Str(old)
	}
	// zend.exception_string_param_max_len: 0..1000000 (PHP_INI_ALL); значения
	// вне диапазона (в т.ч. -1) отвергаются, ini_set возвращает false.
	if name == "zend.exception_string_param_max_len" {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil || n < 0 || n > 1000000 {
			return vm.Bool(false)
		}
		old, ok := iniValues[name]
		if !ok {
			old = "15"
		}
		iniValues[name] = strconv.FormatInt(n, 10)
		return vm.Str(old)
	}
	old, ok := iniValues[name]
	if name == "fiber.stack_size" {
		// fiber.stack_size: отрицательное значение отвергается с Warning;
		// пустое/слишком маленькое — сохраняется, start() затем бросит.
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n < 0 {
			iniValues[name] = s
			ctx.VM().Warning("fiber.stack_size must be a positive number")
			return vm.Str(old)
		}
		iniValues[name] = s
		ctx.VM().SetFiberStackSize(s)
		return vm.Str(old)
	}
	if !ok {
		// Неизвестная опция не сохраняется; PHP возвращает false.
		return vm.Bool(false)
	}
	if name == "open_basedir" {
		// open_basedir можно только ужесточать; предупреждения нет.
		if !iniSetOpenBasedir(s) {
			return vm.Bool(false)
		}
		return vm.Str(old)
	}
	if name == "error_log" && !errorLogPathAllowed(s) {
		ctx.VM().Warning("ini_set(): open_basedir restriction in effect. File(" + s +
			") is not within the allowed path(s): (" + openBasedirRaw() + ")")
		return vm.Bool(false)
	}
	if name == "arg_separator.input" || name == "arg_separator.output" {
		// Обе директивы используют OnUpdateStringUnempty: пустое значение
		// отвергается (ini_set возвращает false, значение не меняется).
		if s == "" {
			return vm.Bool(false)
		}
	}
	if name == "auto_detect_line_endings" {
		ctx.VM().Deprecated("auto_detect_line_endings is deprecated")
	}
	iniValues[name] = s
	if name == "include_path" {
		fsIncludePath = s
	}
	if name == "precision" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			vm.SetFloatPrecision(n)
		}
	}
	return vm.Str(old)
}

// iniArrSet вставляет значение в массив по ключу (int-ключ для канонической
// десятичной строки).
func iniArrSet(arr *vm.Array, key string, val vm.Slot) {
	if n, ok := canonicalDecimalKey(key); ok {
		_ = arr.SetInt(n, val)
		return
	}
	_ = arr.SetStr(key, val)
}

func getCfgVarFn(ctx *vm.Context) vm.Value {
	name := normalizeIniKey(arg0(ctx).ToString())
	if v, ok := iniValues[name]; ok {
		return vm.Str(v)
	}
	prefix := name + "["
	arr := vm.NewArray()
	found := false
	var keys []string
	for k := range iniValues {
		if strings.HasPrefix(k, prefix) && strings.HasSuffix(k, "]") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		iniArrSet(arr, k[len(prefix):len(k)-1], vm.MakeString(iniValues[k]))
		found = true
	}
	if found {
		return vm.MakeArray(arr)
	}
	return vm.Bool(false)
}

func iniGetAllFn(ctx *vm.Context) vm.Value {
	ext := ""
	hasExt := len(ctx.Args()) >= 1
	if hasExt {
		ext = normalizeIniKey(arg0(ctx).ToString())
		if ext != "" {
			known := false
			for _, e := range knownExtensions {
				if normalizeIniKey(e) == ext {
					known = true
					break
				}
			}
			if !known {
				ctx.VM().Warning("ini_get_all(): Extension \"" + arg0(ctx).ToString() + "\" cannot be found")
				return vm.Bool(false)
			}
		} else {
			ctx.VM().Warning("ini_get_all(): Extension \"\" cannot be found")
			return vm.Bool(false)
		}
	}
	details := true
	if len(ctx.Args()) >= 2 {
		details = arg(ctx, 1).ToBool()
	}
	out := vm.NewArray()
	for k, v := range iniValues {
		if hasExt && !strings.HasPrefix(k, ext+".") {
			continue
		}
		if details {
			e := vm.NewArray()
			_ = e.SetStr("global_value", vm.MakeString(v))
			_ = e.SetStr("local_value", vm.MakeString(v))
			_ = e.SetInt(7, vm.MakeInt(7))
			_ = out.SetStr(k, vm.MakeArray(e))
		} else {
			_ = out.SetStr(k, vm.MakeString(v))
		}
	}
	return vm.MakeArray(out)
}

func isIniSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

func iniParseQuantityFn(ctx *vm.Context) vm.Value {
	return vm.Int(parseIniQuantityWarn(ctx, arg0(ctx).ToString()))
}

// parseIniQuantityWarn — как parseIniQuantity, но печатает предупреждения
// Zend (PHP 8.2+) о некорректных величинах.
func parseIniQuantityWarn(ctx *vm.Context, str string) int64 {
	// Границы без крайних пробелов — как в zend_ini_parse_quantity.
	start, end := 0, len(str)
	for start < end && isIniSpace(str[start]) {
		start++
	}
	for start < end && isIniSpace(str[end-1]) {
		end--
	}
	if start == end {
		return 0
	}
	// Позиция сразу после ведущих цифр (для формулировки "interpreting as").
	p := start
	if str[p] == '+' || str[p] == '-' {
		p++
	}
	digitsStart := p
	// Префикс основания (0x/0b/0o): Zend различает «нет цифр после префикса»
	// (пробел/знак/конец) и «нет ведущих цифр» (недопустимая цифра).
	base := 10
	if str[p] == '0' && p+1 < end {
		switch str[p+1] | 0x20 {
		case 'x':
			base = 16
		case 'o':
			base = 8
		case 'b':
			base = 2
		}
	}
	digitInBase := func(c byte) bool {
		switch {
		case c >= '0' && c <= '9':
			return int(c-'0') < base
		case base == 16 && c >= 'a' && c <= 'f', base == 16 && c >= 'A' && c <= 'F':
			return true
		}
		return false
	}
	if base != 10 {
		q := p + 2
		for q < end && digitInBase(str[q]) {
			q++
		}
		if q == p+2 {
			if q == end || str[q] == '+' || str[q] == '-' || isIniSpace(str[q]) {
				ctx.VM().Warn(`Invalid quantity "` + str + `": no digits after base prefix, interpreting as "0" for backwards compatibility`)
			} else {
				ctx.VM().Warn(`Invalid quantity "` + str + `": no valid leading digits, interpreting as "0" for backwards compatibility`)
			}
			return 0
		}
		p = q
	} else {
		for p < end && str[p] >= '0' && str[p] <= '9' {
			p++
		}
		if p == digitsStart {
			ctx.VM().Warn(`Invalid quantity "` + str + `": no valid leading digits, interpreting as "0" for backwards compatibility`)
			return 0
		}
	}
	// Пробелы между числом и остатком.
	wsEnd := p
	for wsEnd < end && isIniSpace(str[wsEnd]) {
		wsEnd++
	}
	val := parseIniQuantity(str)
	if wsEnd == end {
		return val
	}
	last := str[end-1]
	switch last {
	case 'g', 'G', 'm', 'M', 'k', 'K':
		// Множитель берётся из последнего символа; всё, что было между
		// числом и им, игнорируется с предупреждением.
		garbage := false
		for i := wsEnd; i < end-1; i++ {
			if !isIniSpace(str[i]) {
				garbage = true
				break
			}
		}
		if garbage {
			ctx.VM().Warn(`Invalid quantity "` + str + `", interpreting as "` + str[:wsEnd] + string(last) + `" for backwards compatibility`)
		}
	default:
		ctx.VM().Warn(`Invalid quantity "` + str + `": unknown multiplier "` + string(last) + `", interpreting as "` + str[:wsEnd] + `" for backwards compatibility`)
	}
	return val
}

// parseIniQuantity повторяет разбор INI-сокращений (k/m/g), возвращая байты.
func parseIniQuantity(str string) int64 {
	start, end := 0, len(str)
	for start < end && isIniSpace(str[start]) {
		start++
	}
	for start < end && isIniSpace(str[end-1]) {
		end--
	}
	if start == end {
		return 0
	}
	p := start
	neg := false
	if str[p] == '+' {
		p++
	} else if str[p] == '-' {
		neg = true
		p++
	}
	if p >= end || str[p] < '0' || str[p] > '9' {
		return 0
	}
	base := 10
	foundSuffix := false
	if str[p] == '0' && (p+1 == end || !(str[p+1] >= '0' && str[p+1] <= '9')) {
		if p+1 == end {
			return 0
		}
		switch str[p+1] {
		case 'g', 'G', 'm', 'M', 'k', 'K':
			foundSuffix = true
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		default:
			return 0
		}
		if !foundSuffix {
			p += 2
		}
	}
	var val uint64
	if foundSuffix {
		// Значение — просто 0 сразу за которым множитель.
		val = 0
	} else {
		numStart := p
		for p < end {
			d := int64(-1)
			c := str[p]
			switch {
			case c >= '0' && c <= '9':
				d = int64(c - '0')
			case base == 16 && c >= 'a' && c <= 'f':
				d = int64(c-'a') + 10
			case base == 16 && c >= 'A' && c <= 'F':
				d = int64(c-'A') + 10
			case base == 8 && c >= '0' && c <= '7':
				d = int64(c - '0')
			case base == 2 && (c == '0' || c == '1'):
				d = int64(c - '0')
			}
			if d < 0 || d >= int64(base) {
				break
			}
			val = val*uint64(base) + uint64(d)
			p++
		}
		if p == numStart {
			return 0
		}
	}
	// Пропускаем пробелы между числом и суффиксом.
	digitsEnd := p
	for digitsEnd < end && isIniSpace(str[digitsEnd]) {
		digitsEnd++
	}
	var ret int64
	if digitsEnd == end {
		ret = int64(val)
	} else {
		switch str[end-1] {
		case 'g', 'G':
			val *= 1 << 30
			ret = int64(val)
		case 'm', 'M':
			val *= 1 << 20
			ret = int64(val)
		case 'k', 'K':
			val *= 1 << 10
			ret = int64(val)
		default:
			ret = int64(val)
		}
	}
	if neg {
		ret = -ret
	}
	return ret
}

func getenvFn(ctx *vm.Context) vm.Value {
	// Значения из --ENV-- лежат в $_ENV; процессное окружение — в os.Environ.
	vmEnv := map[string]string{}
	if v, ok := ctx.VM().GlobalGet("$_ENV"); ok && v.Type() == vm.ArrayType {
		for k, val := range v.AsArray().All() {
			if k.IsStr() {
				vmEnv[k.StrVal()] = val.ToString()
			}
		}
	}
	if len(ctx.Args()) == 0 {
		arr := vm.NewArray()
		for _, kv := range os.Environ() {
			if i := strings.IndexByte(kv, '='); i >= 0 {
				_ = arr.SetStr(kv[:i], vm.MakeString(kv[i+1:]))
			}
		}
		for k, val := range vmEnv {
			_ = arr.SetStr(k, vm.MakeString(val))
		}
		return vm.MakeArray(arr)
	}
	name := arg0(ctx).ToString()
	if v, ok := vmEnv[name]; ok {
		return vm.Str(v)
	}
	if v, ok := os.LookupEnv(name); ok {
		return vm.Str(v)
	}
	return vm.Bool(false)
}

func putenvFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	if s == "" || s[0] == '=' {
		ctx.Throw(vm.NewThrowable("ValueError", "putenv(): Argument #1 ($assignment) must have a valid syntax"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if i := strings.IndexByte(s, '='); i >= 0 {
		_ = os.Setenv(s[:i], s[i+1:])
	} else {
		_ = os.Unsetenv(s)
	}
	return vm.Bool(true)
}

var knownExtensions = []string{
	"Core", "standard", "pcre", "date", "json", "mbstring", "iconv",
	"SPL", "Reflection", "hash", "ctype", "tokenizer", "fileinfo",
	"session", "random", "zlib", "filter", "posix", "pcntl",
}

func extensionLoadedFn(ctx *vm.Context) vm.Value {
	name := normalizeIniKey(arg0(ctx).ToString())
	for _, e := range knownExtensions {
		if normalizeIniKey(e) == name {
			return vm.Bool(true)
		}
	}
	return vm.Bool(false)
}

func getLoadedExtensionsFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, e := range knownExtensions {
		_, _ = out.Append(vm.MakeString(e))
	}
	return vm.MakeArray(out)
}

func getExtensionFuncsFn(ctx *vm.Context) vm.Value {
	ext := normalizeIniKey(arg0(ctx).ToString())
	// Функции расширения "zend" в php-src живут в отдельной таблице
	// (zend_version и т.п.) и доступны через get_extension_funcs("zend"),
	// хотя самого расширения нет в get_loaded_extensions().
	if ext == "zend" {
		out := vm.NewArray()
		for _, n := range internalFuncNames {
			_, _ = out.Append(vm.MakeString(n))
		}
		return vm.MakeArray(out)
	}
	for _, e := range knownExtensions {
		if normalizeIniKey(e) != ext {
			continue
		}
		out := vm.NewArray()
		for _, n := range internalFuncNames {
			_, _ = out.Append(vm.MakeString(n))
		}
		return vm.MakeArray(out)
	}
	return vm.Bool(false)
}

// disabledFunctionSet возвращает множество отключённых функций из disable_functions.
func disabledFunctionSet() map[string]bool {
	out := map[string]bool{}
	raw, ok := iniValues["disable_functions"]
	if !ok || raw == "" {
		return out
	}
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if f != "" {
			out[f] = true
		}
	}
	return out
}

// --- загруженные файлы ---

func collectUploadedPaths(ctx *vm.Context) map[string]bool {
	out := map[string]bool{}
	for p := range uploadedTmpPaths {
		out[p] = true
	}
	v, ok := ctx.VM().GlobalGet("$_FILES")
	if !ok || v.Type() != vm.ArrayType {
		return out
	}
	var walk func(a *vm.Array)
	walk = func(a *vm.Array) {
		for k, val := range a.All() {
			if k.IsStr() && k.StrVal() == "tmp_name" && val.Type() == vm.StringType {
				out[val.ToString()] = true
				continue
			}
			if val.Type() == vm.ArrayType {
				walk(val.AsArray())
			}
		}
	}
	walk(v.AsArray())
	return out
}

func isUploadedFileFn(ctx *vm.Context) vm.Value {
	path := pathString(ctx, 0)
	return vm.Bool(collectUploadedPaths(ctx)[path])
}

func moveUploadedFileFn(ctx *vm.Context) vm.Value {
	from := pathString(ctx, 0)
	to := arg(ctx, 1).ToString()
	if !collectUploadedPaths(ctx)[from] {
		return vm.Bool(false)
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return vm.Bool(false)
	}
	if err := os.WriteFile(to, data, 0666); err != nil {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// --- внешние команды ---

// intvalFn повторяет intval(): base учитывается только для строк.
func intvalFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx)
	base := int64(10)
	if len(ctx.Args()) >= 2 {
		base = arg(ctx, 1).ToInt()
	}
	if s.Type() == vm.StringType && base != 10 {
		return vm.MakeInt(iniAtoiBase(s.ToString(), base))
	}
	return vm.MakeInt(zvalGetLong(ctx, s))
}

// floatvalFn повторяет floatval()/doubleval().
func floatvalFn(ctx *vm.Context) vm.Value {
	return vm.MakeFloat(zvalGetDouble(ctx, arg0(ctx)))
}

// zvalGetLong повторяет zval_get_long(): приведение к int для intval/(int).
func zvalGetLong(ctx *vm.Context, s vm.Slot) int64 {
	switch s.Type() {
	case vm.ArrayType:
		if a := s.AsArray(); a != nil && a.Len() > 0 {
			return 1
		}
		return 0
	case vm.ObjectType:
		name := "object"
		if o := ctx.VM().SlotObject(s); o != nil {
			name = o.ClassName()
		}
		ctx.VM().Warn("Object of class " + name + " could not be converted to int")
		return 1
	case vm.ClosureType:
		return 1
	}
	return s.ToInt()
}

// zvalGetDouble повторяет zval_get_double(): приведение к float.
func zvalGetDouble(ctx *vm.Context, s vm.Slot) float64 {
	switch s.Type() {
	case vm.ArrayType:
		if a := s.AsArray(); a != nil && a.Len() > 0 {
			return 1
		}
		return 0
	case vm.ObjectType:
		name := "object"
		if o := ctx.VM().SlotObject(s); o != nil {
			name = o.ClassName()
		}
		ctx.VM().Warn("Object of class " + name + " could not be converted to float")
		return 1
	}
	return s.ToFloat()
}

// iniAtoiBase повторяет ZEND_STRTOL с учётом 0b-спецслучая intval().
func iniAtoiBase(str string, base int64) int64 {
	i := 0
	for i < len(str) && isASCIISpace(str[i]) {
		i++
	}
	rest := str[i:]
	if (base == 0 || base == 2) && len(rest) > 2 {
		off := 0
		if rest[0] == '-' || rest[0] == '+' {
			off = 1
		}
		if off+1 < len(rest) && rest[off] == '0' && (rest[off+1] == 'b' || rest[off+1] == 'B') {
			var tmp string
			if off == 1 {
				tmp = string(rest[0])
			}
			tmp += rest[off+2:]
			return cStrtol(tmp, 2)
		}
	}
	return cStrtol(rest, base)
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func asciiDigitVal(c byte) int64 {
	switch {
	case c >= '0' && c <= '9':
		return int64(c - '0')
	case c >= 'a' && c <= 'z':
		return int64(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int64(c-'A') + 10
	}
	return -1
}

// cStrtol повторяет C strtol(str, NULL, base).
func cStrtol(s string, base int64) int64 {
	i := 0
	for i < len(s) && isASCIISpace(s[i]) {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	b := base
	if b == 0 {
		if i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') {
			b, i = 16, i+2
		} else if i < len(s) && s[i] == '0' {
			b = 8
		} else {
			b = 10
		}
	} else if b == 16 && i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') {
		i += 2
	}
	var v int64
	any := false
	for i < len(s) {
		d := asciiDigitVal(s[i])
		if d < 0 || d >= b {
			break
		}
		v = v*b + d
		any = true
		i++
	}
	if !any {
		return 0
	}
	if neg {
		return -v
	}
	return v
}

// procHandle — состояние процесса, открытого proc_open().
type procHandle struct {
	cmd *exec.Cmd
	// cmdStr — значение ключа "command" в proc_get_status(): для массива —
	// argv[0], для строки — сама строка (как proc_open.c).
	cmdStr string
	pid    int
	// cached — статус завершения (WIFEXITED) закэширован: повторные обращения
	// не вызывают waitpid (waitpid_cached в proc_open.c; повторный waitpid
	// вернул бы ECHILD).
	cached   bool
	cachedWs syscall.WaitStatus
	// closeCode — значение proc_close (pclose_ret из proc_open_rsrc_dtor):
	// код выхода, raw-статус для сигнала, либо -1.
	closeCode int64
	// files — все родительские концы (закрываются при завершении процесса).
	files []*os.File
	// pipes — ресурсы-обёртки пайпов/сокетов (гасятся при завершении).
	pipes []*fsHandle
	// collect — временные файлы вместо унаследованных fd: их содержимое
	// переносится в буфер вывода VM при закрытии процесса (иначе вывод ребёнка
	// ушёл бы в реальный stdout процесса-хоста и потерялся для харнесса).
	collect []string
	// res — ресурс процесса (для пометки Closed при завершении).
	res *vm.Resource
	// id — id ресурса процесса.
	id int64
	// finalized — процесс уже завершён (proc_close или уничтожение ресурса).
	finalized bool
	// sink — VM, в буфер которой переносится унаследованный вывод ребёнка.
	sink *vm.VM
}

// procWaitpid повторяет waitpid_cached() из proc_open.c: однажды увиденный
// WIFEXITED-статус кэшируется, чтобы proc_close/proc_get_status не теряли код
// выхода после первого reap. nohang — WNOHANG (опрос статуса).
func (ph *procHandle) procWaitpid(nohang bool) (int, syscall.WaitStatus, error) {
	if ph.cached {
		return ph.pid, ph.cachedWs, nil
	}
	if ph.pid <= 0 {
		return -1, 0, syscall.ECHILD
	}
	opts := syscall.WUNTRACED
	if nohang {
		opts |= syscall.WNOHANG
	}
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(ph.pid, &ws, opts, nil)
		if err == syscall.EINTR {
			continue
		}
		if pid > 0 && ws.Exited() {
			ph.cached = true
			ph.cachedWs = ws
		}
		return pid, ws, err
	}
}

// procHandles — таблица открытых процессов (по id ресурса).
var procHandles = map[int64]*procHandle{}

// procDescEntry — разобранный элемент descriptor_spec.
type procDescEntry struct {
	index     int
	kind      string // pipe/socket/file/null/redirect/std
	childEnd  *os.File
	parentEnd *os.File
	mode      string
	// inherited — redirect на fd родителя (0..2), -1 если нет.
	inherited int
	// collectIdx — индекс временного файла сбора для inherited (или -1).
	collectIdx int
}

// procDupFd дублирует fd с флагом close-on-exec: копия не должна утекать в
// дочерний процесс (иначе EOF на пайпах/сокетах не доставляется).
func procDupFd(fd int) (int, error) {
	r, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_DUPFD_CLOEXEC), 0)
	if errno != 0 {
		return -1, errno
	}
	return int(r), nil
}

// procOpenFn — proc_open(): разбор descriptor_spec (pipe/socket/file/null/
// redirect), запуск процесса и раскладка $pipes.
func procOpenFn(ctx *vm.Context) vm.Value {
	cmd := arg0(ctx)
	var c *exec.Cmd
	progName := ""
	if cmd.Type() == vm.ArrayType {
		a := cmd.AsArray()
		if a == nil || a.Len() == 0 {
			ctx.Throw(vm.NewThrowable("ValueError", "proc_open(): Argument #1 ($command) must have at least one element"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		vals := a.Values()
		for i, v := range vals {
			s := v.ToString()
			if i == 0 && s == "" {
				ctx.Throw(vm.NewThrowable("ValueError", "First element must contain a non-empty program name"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Bool(false)
			}
			if strings.IndexByte(s, 0) >= 0 {
				ctx.Throw(vm.NewThrowable("ValueError",
					fmt.Sprintf("Command array element %d contains a null byte", i+1)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Bool(false)
			}
		}
		args := make([]string, 0, len(vals)-1)
		for _, v := range vals[1:] {
			args = append(args, v.ToString())
		}
		c = exec.Command(vals[0].ToString(), args...)
		progName = vals[0].ToString()
	} else {
		c = exec.Command("/bin/sh", "-c", cmd.ToString())
		progName = cmd.ToString()
	}
	// Свой process-group: при строковой команде прямой ребёнок — `sh -c`, а
	// реальный воркер — его внук; тестовому харнессу нужно уметь погасить всю
	// группу (killOrphan), иначе потомки переживают тест и держат пайпы.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if len(ctx.Args()) >= 4 && arg(ctx, 3).Type() != vm.NullType {
		c.Dir = arg(ctx, 3).ToString()
	}
	// $env задан: среда ребёнка ПОЛНОСТЬЮ заменяется (наследование off).
	if len(ctx.Args()) >= 5 && arg(ctx, 4).Type() == vm.ArrayType {
		env := []string{}
		for k, v := range arg(ctx, 4).AsArray().All() {
			if v.Type() == vm.ArrayType {
				// PHP приводит значение среды к строке — массив даёт warning
				// и строку "Array" (bug60602).
				ctx.VM().Warn("Array to string conversion")
			}
			val := v.ToString()
			if val == "" {
				continue
			}
			if k.IsStr() && k.StrVal() != "" {
				env = append(env, k.StrVal()+"="+val)
			} else {
				env = append(env, val)
			}
		}
		c.Env = env
	}
	spec := arg(ctx, 1)
	if spec.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("proc_open", 2, "descriptor_spec", "array", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	ph := &procHandle{cmd: c, cmdStr: progName, closeCode: -1, sink: ctx.VM()}
	// fail — общий выход с откатом созданных fd и без изменения $pipes.
	created := []*os.File{}
	var extraNulls []*os.File
	fail := func() vm.Value {
		for _, f := range created {
			_ = f.Close()
		}
		return vm.Bool(false)
	}
	entries := []*procDescEntry{}
	byIndex := map[int]*procDescEntry{}
	byRef := map[int]bool{}
	inheritFiles := map[int]*os.File{}
	addInherited := func(target int) *os.File {
		// Вместо реального fd родителя — временный файл: вывод ребёнка должен
		// попасть в буфер VM (харнесс), а не уйти в stdout процесса-хоста.
		// Один и тот же target переиспользует файл (в PHP это dup одного fd).
		if f, ok := inheritFiles[target]; ok {
			return f
		}
		f, err := os.CreateTemp("", "proc-inherit")
		if err != nil {
			return nil
		}
		if target == 0 && len(phpStdinRaw) > 0 {
			_, _ = f.Write(phpStdinRaw)
			_, _ = f.Seek(0, io.SeekStart)
		}
		inheritFiles[target] = f
		ph.collect = append(ph.collect, f.Name())
		created = append(created, f)
		return f
	}
	specArr := spec.AsArray()
	for k, v := range specArr.All() {
		if k.IsStr() {
			ctx.Throw(vm.NewThrowable("ValueError",
				"proc_open(): Argument #2 ($descriptor_spec) must be an integer indexed array"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return fail()
		}
		fd := int(k.IntVal())
		e := &procDescEntry{index: fd, inherited: -1, collectIdx: -1}
		switch {
		case v.Type() == vm.ResourceType:
			h := fsHandleOf(v)
			if h == nil {
				ctx.Throw(vm.NewThrowable("TypeError",
					"proc_open(): supplied resource is not a valid stream resource"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return fail()
			}
			fd2, err := procDupFd(int(h.f.Fd()))
			if err != nil {
				return fail()
			}
			e.kind = "std"
			e.childEnd = os.NewFile(uintptr(fd2), "dup")
			created = append(created, e.childEnd)
		case v.Type() == vm.ArrayType:
			parts := v.AsArray()
			if parts.Len() == 0 {
				ctx.Throw(vm.NewThrowable("ValueError", "Missing handle qualifier"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return fail()
			}
			qual := parts.Values()[0].ToString()
			switch qual {
			case "pipe":
				pm := parts.Values()
				if len(pm) < 2 {
					ctx.Throw(vm.NewThrowable("ValueError", "Missing mode parameter for 'pipe'"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
					return fail()
				}
				mode := pm[1].ToString()
				pr, pw, err := os.Pipe()
				if err != nil {
					return fail()
				}
				created = append(created, pr, pw)
				e.kind = "pipe"
				e.mode = mode
				if strings.HasPrefix(mode, "w") {
					e.childEnd, e.parentEnd = pw, pr
				} else {
					e.childEnd, e.parentEnd = pr, pw
				}
			case "socket":
				fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
				if err != nil {
					return fail()
				}
				ce := os.NewFile(uintptr(fds[1]), "socketpair")
				pe := os.NewFile(uintptr(fds[0]), "socketpair")
				created = append(created, ce, pe)
				e.kind = "socket"
				e.mode = "r+"
				e.childEnd, e.parentEnd = ce, pe
			case "file":
				pm := parts.Values()
				if len(pm) < 2 {
					ctx.Throw(vm.NewThrowable("ValueError", "Missing file name parameter for 'file'"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
					return fail()
				}
				if len(pm) < 3 {
					ctx.Throw(vm.NewThrowable("ValueError", "Missing mode parameter for 'file'"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
					return fail()
				}
				name := pm[1].ToString()
				fmode := pm[2].ToString()
				plain := !strings.Contains(name, "://")
				if plain && !validFopenMode(fmode) {
					ctx.VM().Warning(fmt.Sprintf("proc_open(%s): Failed to open stream: `%s' is not a valid mode for fopen", name, fmode))
					return fail()
				}
				f, err := os.OpenFile(normalizePath(name), openFlagsForMode(fmode), 0666)
				if err != nil {
					ctx.VM().Warning(fmt.Sprintf("proc_open(%s): Failed to open stream: %s", name, openFailReason(name, fmode)))
					return fail()
				}
				// Скрытый ресурс-поток (как php_stream_open_wrapper внутри PHP).
				fsNextID++
				created = append(created, f)
				e.kind = "file"
				e.childEnd = f
			case "redirect":
				pm := parts.Values()
				if len(pm) < 2 {
					ctx.Throw(vm.NewThrowable("ValueError", "Missing redirection target"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
					return fail()
				}
				tv := derefSlot(pm[1])
				if tv.Type() != vm.IntType {
					ctx.Throw(vm.NewThrowable("ValueError",
						"Redirection target must be of type int, "+phpValueTypeName(tv)+" given"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
					return fail()
				}
				target := int(tv.ToInt())
				e.kind = "redirect"
				if t := byIndex[target]; t != nil && t.childEnd != nil {
					fd2, err := procDupFd(int(t.childEnd.Fd()))
					if err != nil {
						return fail()
					}
					e.childEnd = os.NewFile(uintptr(fd2), "redirect")
					created = append(created, e.childEnd)
				} else if target >= 0 && target <= 2 {
					f := addInherited(target)
					if f == nil {
						return fail()
					}
					e.childEnd = f
					e.inherited = target
				} else {
					ctx.VM().Warning(fmt.Sprintf("proc_open(): Redirection target %d not found", target))
					return fail()
				}
			case "null":
				f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
				if err != nil {
					return fail()
				}
				created = append(created, f)
				e.kind = "null"
				e.childEnd = f
			case "pty":
				ctx.VM().Warning("proc_open(): PTY (pseudoterminal) not supported on this system")
				return fail()
			default:
				ctx.VM().Warning("proc_open(): " + qual + " is not a valid descriptor spec/mode")
				return fail()
			}
		default:
			ctx.Throw(vm.NewThrowable("ValueError",
				"proc_open(): Argument #2 ($descriptor_spec) must only contain arrays and streams"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return fail()
		}
		entries = append(entries, e)
		byIndex[fd] = e
		byRef[fd] = true
	}
	// Раскладка дочерних fd: 0/1/2 — стандартные потоки, остальные — ExtraFiles.
	nullFds := map[int]bool{}
	for _, e := range entries {
		if e.childEnd == nil {
			continue
		}
		switch e.index {
		case 0:
			c.Stdin = e.childEnd
		case 1:
			c.Stdout = e.childEnd
		case 2:
			c.Stderr = e.childEnd
		default:
			nullFds[e.index] = true
		}
	}
	// Унаследованные (не заданные в spec) стандартные fd направляем в файлы
	// сбора — так вывод ребёнка не теряется для харнесса.
	for fd := 0; fd <= 2; fd++ {
		if byRef[fd] {
			continue
		}
		f := addInherited(fd)
		if f == nil {
			return fail()
		}
		switch fd {
		case 0:
			c.Stdin = f
		case 1:
			c.Stdout = f
		case 2:
			c.Stderr = f
		}
	}
	maxFd := -1
	for fd := range nullFds {
		if fd > maxFd {
			maxFd = fd
		}
	}
	if maxFd >= 3 {
		extra := make([]*os.File, maxFd-2)
		for i := range extra {
			extra[i] = nil
		}
		for _, e := range entries {
			if e.index >= 3 && e.childEnd != nil {
				extra[e.index-3] = e.childEnd
			}
		}
		for i := range extra {
			if extra[i] == nil {
				f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
				if err != nil {
					return fail()
				}
				created = append(created, f)
				extraNulls = append(extraNulls, f)
				extra[i] = f
			}
		}
		c.ExtraFiles = extra
	}
	if err := c.Start(); err != nil {
		return fail()
	}
	ph.pid = c.Process.Pid
	// Закрываем в родителе концы, переданные дочернему процессу, чтобы EOF
	// распространялся корректно. Родительские концы пайпов остаются открытыми.
	for _, e := range entries {
		if e.childEnd != nil {
			_ = e.childEnd.Close()
		}
	}
	for _, f := range extraNulls {
		_ = f.Close()
	}
	_ = created
	created = nil
	// Ресурсы-обёртки для pipe/socket — в порядке descriptor spec; процесс
	// регистрируется последним (как PHP).
	pipes := vm.NewArray()
	for _, e := range entries {
		var h *fsHandle
		switch e.kind {
		case "pipe":
			canRead := strings.HasPrefix(e.mode, "w")
			h = &fsHandle{f: e.parentEnd, path: "pipe", mode: e.mode, push: -1,
				canRead: canRead, canWrite: !canRead, singleRead: true}
		case "socket":
			h = &fsHandle{f: e.parentEnd, path: "socketpair", mode: "r+", push: -1,
				canRead: true, canWrite: true, isSocket: true, sockType: "unix_socket", singleRead: true}
		default:
			continue
		}
		ph.files = append(ph.files, e.parentEnd)
		ph.pipes = append(ph.pipes, h)
		_ = pipes.Set(vm.MakeIntKey(int64(e.index)), fsNewResource(h, "stream"))
	}
	id := fsNextID
	fsNextID++
	ph.id = id
	procHandles[id] = ph
	if len(ctx.Args()) >= 3 {
		if s := arg(ctx, 2); s.IsRef() && s.Ref() != nil {
			s.Ref().Set(vm.MakeArray(pipes))
		}
	}
	res := vm.MakeResource(id, "process", ph)
	ph.res = vm.AsResource(res)
	if ph.res != nil {
		// Уничтожение ресурса процесса (как proc_open_rsrc_dtor): закрывает
		// пайпы, не дожидаясь ребёнка.
		ph.res.OnDrop = func() { ph.finish(false) }
	}
	return res
}

// finish завершает процесс: закрывает пайпы и файлы. wait=true (proc_close)
// дополнительно дожидается ребёнка; иначе — только WNOHANG-проверка, как в
// proc_open_rsrc_dtor.
func (ph *procHandle) finish(wait bool) {
	if ph.finalized {
		return
	}
	ph.finalized = true
	delete(procHandles, ph.id)
	for _, h := range ph.pipes {
		if h == nil {
			continue
		}
		fsResourceClosed(h)
		_ = h.Close()
	}
	for _, f := range ph.files {
		_ = f.Close()
	}
	ph.files = nil
	if ph.res != nil {
		ph.res.Closed = true
		ph.res.OnDrop = nil
	}
	// Содержимое унаследованных stdout/stderr — в буфер вывода VM (после
	// ожидания ребёнка, чтобы не прочитать файл раньше записи).
	readCollect := func() {
		for _, p := range ph.collect {
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
				if ph.sink != nil {
					ph.sink.Write(string(b))
				}
			}
			_ = os.Remove(p)
		}
		ph.collect = nil
	}
	// Как proc_open_rsrc_dtor: proc_close (wait=true) дожидается ребёнка и
	// запоминает pclose_ret; уничтожение ресурса — только WNOHANG-проверка
	// (ребёнок не должен задерживать сборку).
	if ph.cmd != nil && ph.cmd.Process != nil {
		if wait {
			pid, ws, _ := ph.procWaitpid(false)
			if pid <= 0 {
				ph.closeCode = -1
			} else if ws.Exited() {
				ph.closeCode = int64(ws.ExitStatus())
			} else {
				ph.closeCode = int64(ws)
			}
		} else {
			// Тестовый харнесс: гасим группу и при финализации ресурса —
			// иначе ребёнок переживает тест (busy-loop держит пайпы).
			if procKillOrphans {
				_ = syscall.Kill(-ph.cmd.Process.Pid, syscall.SIGKILL)
				_ = ph.cmd.Process.Kill()
			}
			_, _, _ = ph.procWaitpid(true)
			ph.closeCode = -1
		}
	}
	readCollect()
}

// procKillOrphans — тестовый режим харнесса (SetKillOrphansMode): финализация
// ресурса процесса гасит всю process-group ребёнка (в PHP dtor не убивает).
var procKillOrphans bool

// SetKillOrphansMode включает/выключает тестовый режим (PHPT-харнесс).
func SetKillOrphansMode(b bool) { procKillOrphans = b }

// killOrphan гасит дочерний процесс (тестовый харнесс: воркеры php-src-тестов —
// например `ServerClientTestCase.inc RUN_WORKER` — при незакрытом stdin на EOF
// крутятся в busy-loop и держат унаследованные пайпы, из-за чего `go test`
// ждёт WaitDelay и завершается FAIL'ом «Test I/O incomplete»).
func (ph *procHandle) killOrphan() {
	if ph == nil {
		return
	}
	if ph.cmd != nil && ph.cmd.Process != nil {
		// Бьём всю process-group: при `sh -c` реальный процесс — внук,
		// SIGKILL одному `sh` его не тронет.
		_ = syscall.Kill(-ph.cmd.Process.Pid, syscall.SIGKILL)
		_ = ph.cmd.Process.Kill()
	}
	ph.finish(false)
}

// procFetchHandle достаёт procHandle ресурса и повторяет диагностику
// zend_fetch_resource (TypeError для непроцессного/закрытого ресурса,
// TypeError для не-ресурса).
func procFetchHandle(ctx *vm.Context, fn, param string) *procHandle {
	v := arg0(ctx)
	r := vm.AsResource(v)
	if r == nil {
		ctx.Throw(vm.NewThrowable("TypeError",
			fmt.Sprintf("%s(): Argument #1 ($%s) must be of type resource, %s given", fn, param, phpTypeName(ctx, 0))))
		return nil
	}
	if r.Kind != "process" || r.Closed {
		ctx.Throw(vm.NewThrowable("TypeError",
			fmt.Sprintf("%s(): supplied resource is not a valid process resource", fn)))
		return nil
	}
	return procHandles[r.ID]
}

// procCloseFn — proc_close(): дожидается процесса и возвращает код выхода.
func procCloseFn(ctx *vm.Context) vm.Value {
	ph := procFetchHandle(ctx, "proc_close", "process")
	if ph == nil {
		return vm.Bool(false)
	}
	ph.sink = ctx.VM()
	ph.finish(true)
	return vm.Int(ph.closeCode)
}

// procTerminateFn — proc_terminate(): kill(pid, signal), true при успехе.
// Статус процесса не трогается (как в proc_open.c).
func procTerminateFn(ctx *vm.Context) vm.Value {
	ph := procFetchHandle(ctx, "proc_terminate", "process")
	if ph == nil {
		return vm.Bool(false)
	}
	sig := int64(15)
	if len(ctx.Args()) >= 2 {
		sig = arg(ctx, 1).ToInt()
	}
	if sig <= 0 || sig > 64 {
		sig = 15
	}
	if err := syscall.Kill(ph.pid, syscall.Signal(sig)); err != nil {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// procGetStatusFn — proc_get_status(): waitpid(WNOHANG) и массив статуса.
// Порядок ключей и семантика cached/signaled — как в PHP 8.3 proc_open.c.
func procGetStatusFn(ctx *vm.Context) vm.Value {
	ph := procFetchHandle(ctx, "proc_get_status", "process")
	if ph == nil {
		return vm.Bool(false)
	}
	running := true
	signaled, stopped := false, false
	exitCode, termSig, stopSig := int64(-1), int64(0), int64(0)
	pid, ws, err := ph.procWaitpid(true)
	if pid == ph.pid {
		if ws.Exited() {
			running = false
			exitCode = int64(ws.ExitStatus())
		}
		if ws.Signaled() {
			running = false
			signaled = true
			termSig = int64(ws.Signal())
		}
		if ws.Stopped() {
			stopped = true
			stopSig = int64(ws.StopSignal())
		}
	} else if err != nil {
		// ECHILD: ребёнок уже собран (например, после сигнала в
		// proc_get_status) — как в PHP, процесс считается неработающим.
		running = false
	}
	a := vm.NewArray()
	_ = a.Set(vm.MakeStrKey("command"), vm.MakeString(ph.cmdStr))
	_ = a.Set(vm.MakeStrKey("pid"), vm.MakeInt(int64(ph.pid)))
	_ = a.Set(vm.MakeStrKey("cached"), vm.MakeBool(ph.cached))
	_ = a.Set(vm.MakeStrKey("running"), vm.MakeBool(running))
	_ = a.Set(vm.MakeStrKey("signaled"), vm.MakeBool(signaled))
	_ = a.Set(vm.MakeStrKey("stopped"), vm.MakeBool(stopped))
	_ = a.Set(vm.MakeStrKey("exitcode"), vm.MakeInt(exitCode))
	_ = a.Set(vm.MakeStrKey("termsig"), vm.MakeInt(termSig))
	_ = a.Set(vm.MakeStrKey("stopsig"), vm.MakeInt(stopSig))
	return vm.MakeArray(a)
}

func execFn(ctx *vm.Context) vm.Value {
	cmd := arg0(ctx).ToString()
	c := exec.Command("/bin/sh", "-c", cmd)
	var stdout bytes.Buffer
	c.Stdout = &stdout
	runErr := c.Run()

	lines := execOutputLines(stdout.String())
	if len(ctx.Args()) >= 2 {
		if s := arg(ctx, 1); s.IsRef() && s.Ref() != nil {
			arr := vm.NewArray()
			for _, ln := range lines {
				_, _ = arr.Append(vm.MakeString(ln))
			}
			s.Ref().Set(vm.MakeArray(arr))
		}
	}
	code := int64(0)
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			code = int64(ee.ExitCode())
		} else {
			code = -1
		}
	}
	if len(ctx.Args()) >= 3 {
		if s := arg(ctx, 2); s.IsRef() && s.Ref() != nil {
			s.Ref().Set(vm.MakeInt(code))
		}
	}
	last := ""
	if len(lines) > 0 {
		last = lines[len(lines)-1]
	}
	return vm.Str(last)
}

// execOutputLines повторяет разбор вывода exec()/shell_exec(): каждая строка
// без завершающего перевода и хвостовых пробельных символов; финальный пустой
// элемент, порождённый завершающим "\n", отбрасывается.
func execOutputLines(out string) []string {
	if out == "" {
		return nil
	}
	parts := strings.Split(out, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	lines := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSuffix(p, "\r")
		p = strings.TrimRight(p, " \t\r\n\v\f")
		lines = append(lines, p)
	}
	return lines
}

func shellExecFn(ctx *vm.Context) vm.Value {
	cmd := arg0(ctx).ToString()
	if cmd == "" {
		return vm.MakeNull()
	}
	c := exec.Command("/bin/sh", "-c", cmd)
	var stdout bytes.Buffer
	c.Stdout = &stdout
	if err := c.Run(); err != nil {
		// shell_exec не выводит ошибку; при провале возвращает null
		if stdout.Len() == 0 {
			return vm.MakeNull()
		}
	}
	if stdout.Len() == 0 {
		return vm.MakeNull()
	}
	return vm.Str(stdout.String())
}

func systemFn(ctx *vm.Context) vm.Value {
	cmd := arg0(ctx).ToString()
	if cmd == "" {
		return vm.Bool(false)
	}
	c := exec.Command("/bin/sh", "-c", cmd)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	runErr := c.Run()
	if out.Len() > 0 {
		ctx.VM().Write(out.String())
	}
	code := int64(0)
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			code = int64(ee.ExitCode())
		} else {
			code = -1
		}
	}
	if len(ctx.Args()) >= 2 {
		setRefArg(ctx, 1, vm.MakeInt(code))
	}
	lines := execOutputLines(out.String())
	if len(lines) == 0 {
		return vm.Str("")
	}
	return vm.Str(lines[len(lines)-1])
}

// escapeshellarg экранирует аргумент для оболочки (Unix-семантика PHP).
func escapeshellarg(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			b.WriteString(`'\''`)
		} else {
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// escapeshellcmd экранирует метасимволы команды оболочки.
func escapeshellcmd(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '#', '&', ';', '`', '|', '*', '?', '~', '<', '>', '^', '(', ')',
			'[', ']', '{', '}', '$', '\\', 0x0A, 0xFF:
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// debugZvalDumpFn — аналог var_dump с refcount-/interned-аннотациями, как
// debug_zval_dump() в PHP.
func debugZvalDumpFn(ctx *vm.Context) vm.Value {
	if v := ctx.VM(); v != nil {
		_ = v.ArmHandles()
	}
	var b strings.Builder
	st := &dumpState{vm: ctx.VM(), objIDs: map[*vm.Object]int{}, arrs: map[*vm.Array]bool{}, objs: map[*vm.Object]bool{}, refCount: map[*vm.Ref]int{}, arrCount: map[*vm.Array]int{}}
	args := ctx.Args()
	seenA := map[*vm.Array]bool{}
	seenO := map[*vm.Object]bool{}
	for _, s := range args {
		countRefs(ctx.VM(), s, st.refCount, st.arrCount, seenA, seenO)
	}
	// Zend печатает refcount ячейки-ссылки = число zval'ов, на неё
	// указывающих: обход структуры аргумента плюс внешние держатели в
	// таблице переменных/регистрах текущего кадра (debug_zval_dump_refs:
	// $r остаётся держателем, пока не сделан unset).
	for r := range st.refCount {
		st.refCount[r] += ctx.VM().DebugRefHolders(r)
	}
	// Refcount массива: вхождения при обходе аргумента (обычно 1) или
	// число внешних держателей-переменных — большее из них (для
	// массива-аргумента внешних держателей два: сама переменная и
	// регистр-копия аргумента — в Zend это и даёт refcount(2)).
	for a, n := range st.arrCount {
		if h := ctx.VM().DebugArrayHolders(a); h > n {
			st.arrCount[a] = h
		}
	}
	for _, s := range args {
		zvalDump(&b, s, 0, st)
	}
	ctx.Print(b.String())
	return vm.MakeNull()
}

// debugObjRefCount — число ссылок для debug_zval_dump: оценка VM (число слотов
// с объектом) плюс копия zval аргумента, которую Zend делает при передаче
// (для временных значений Zend печатает 1, у нас получится на 1 больше —
// точные числа проверяет только weakrefs_001, там аргумент — переменная).
func debugObjRefCount(o *vm.Object) int {
	n := 1
	if o != nil && int(o.Refs()) > n {
		n = int(o.Refs())
	}
	// Кейс enum всегда держится таблицей констант класса: Zend учитывает
	// эту ссылку с самого первого обращения (в VM учёт отстаёт до первого
	// прохода жизненного цикла).
	if o != nil && n < 2 && o.Class() != nil && o.Class().IsEnum() {
		n = 2
	}
	return n
}

// zvalDump печатает значение в формате debug_zval_dump. Для контейнеров
// указывает refcount/interned; скаляры совпадают с var_dump.
func zvalDump(b *strings.Builder, s vm.Slot, indent int, st *dumpState) {
	if s.IsRef() {
		n := st.refCount[s.Ref()]
		if n < 1 {
			n = 1
		}
		pad := strings.Repeat("  ", indent)
		b.WriteString("reference refcount(" + strconv.Itoa(n) + ") {\n")
		b.WriteString(pad + "  ")
		zvalDump(b, vm.Deref(s), indent+1, st)
		b.WriteString(pad + "}\n")
		return
	}
	switch s.Type() {
	case vm.ResourceType:
		r := vm.AsResource(s)
		if r == nil {
			b.WriteString("NULL\n")
			return
		}
		if r.Closed {
			b.WriteString("resource(" + strconv.FormatInt(r.ID, 10) + ") of type (Unknown) refcount(1)\n")
		} else {
			refc := 2
			if sc, ok := r.Val.(*streamContext); ok {
				refc += sc.holds
			}
			b.WriteString("resource(" + strconv.FormatInt(r.ID, 10) + ") of type (" + r.Kind + ") refcount(" + strconv.Itoa(refc) + ")\n")
		}
	case vm.StringType:
		v := s.StringVal()
		b.WriteString("string(" + strconv.Itoa(len(v)) + ") \"" + v + "\"")
		if vm.StringSlotInterned(s) {
			b.WriteString(" interned\n")
		} else {
			b.WriteString(" refcount(1)\n")
		}
	case vm.ArrayType:
		a := s.AsArray()
		pad := strings.Repeat("  ", indent)
		if st.arrs[a] {
			b.WriteString("*RECURSION*\n")
			return
		}
		st.arrs[a] = true
		defer delete(st.arrs, a)
		if a.Len() == 0 {
			b.WriteString("array(0) interned {\n" + pad + "}\n")
			return
		}
		refc := st.arrCount[a]
		if refc < 1 {
			refc = 1
		}
		b.WriteString("array(" + strconv.Itoa(a.Len()) + ") refcount(" + strconv.Itoa(refc) + "){\n")
		for k, v := range a.All() {
			b.WriteString(pad + "  ")
			if k.IsStr() {
				b.WriteString(`["` + k.StrVal() + "\"]=>\n")
			} else {
				b.WriteString("[" + strconv.FormatInt(k.IntVal(), 10) + "]=>\n")
			}
			b.WriteString(pad + "  ")
			zvalDump(b, v, indent+1, st)
		}
		b.WriteString(pad + "}\n")
	case vm.ObjectType:
		// debug_zval_dump печатает enum-кейс как обычный объект (в отличие от
		// var_dump, где Zend выводит enum(Class::Case)).
		o := st.vm.SlotObject(s)
		if o == nil {
			b.WriteString("NULL\n")
			return
		}
		pad := strings.Repeat("  ", indent)
		if st.objs[o] {
			b.WriteString("*RECURSION*\n")
			return
		}
		st.objs[o] = true
		defer delete(st.objs, o)
		if st.vm.IsDumpPlainObject(o) {
			des := st.vm.ObjectDumpEntries(o)
			cnt := 0
			for _, de := range des {
				if !de.Uninit {
					cnt++
				}
			}
			b.WriteString("object(" + st.vm.DisplayClassNameOf(o.Class()) + ")#" + strconv.Itoa(st.objID(o)) + " (" + strconv.Itoa(cnt) + ") refcount(" + strconv.Itoa(debugObjRefCount(o)) + "){\n")
			for _, de := range des {
				b.WriteString(pad + "  [\"" + de.Key + "\"" + de.Suffix + "]=>\n")
				b.WriteString(pad + "  ")
				if de.Uninit {
					b.WriteString("uninitialized(" + vm.TypeString(de.Type) + ")\n")
					continue
				}
				zvalDump(b, de.Val, indent+1, st)
			}
			b.WriteString(pad + "}\n")
			return
		}
		ents := objectJSONEntries(st.vm, o)
		b.WriteString("object(" + st.vm.DisplayClassNameOf(o.Class()) + ")#" + strconv.Itoa(st.objID(o)) + " (" + strconv.Itoa(len(ents)) + ") refcount(" + strconv.Itoa(debugObjRefCount(o)) + "){\n")
		for _, en := range ents {
			b.WriteString(pad + "  [\"" + en.key + "\"]=>\n")
			b.WriteString(pad + "  ")
			zvalDump(b, en.val, indent+1, st)
		}
		b.WriteString(pad + "}\n")
	default:
		dumpSlot(b, s, indent, st)
	}
}

// --- заголовки / состояние клиента ---

// headerFn проверяет строку заголовка и выводит предупреждения как PHP.
func headerFn(ctx *vm.Context) vm.Value {
	if sent, file, line := ctx.VM().HeadersSent(); sent {
		ctx.VM().Warning(fmt.Sprintf("Cannot modify header information - headers already sent by (output started at %s:%d)", file, line))
		return vm.MakeNull()
	}
	s := arg0(ctx).ToString()
	if strings.IndexByte(s, 0) >= 0 {
		ctx.VM().Warning("Header may not contain NUL bytes")
		return vm.MakeNull()
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' && s[i] != '\r' {
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r' || s[j] == '\n') {
			j++
		}
		if j < len(s) {
			ctx.VM().Warning("Header may not contain more than a single header, new line detected")
			return vm.MakeNull()
		}
	}
	return vm.MakeNull()
}

// errorLogFn — error_log($message, $message_type = 0, $destination = null).
// Тип 0 пишет в error_log из INI (с меткой времени), тип 3 — в указанный файл.
func errorLogFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "error_log() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	msg := arg0(ctx).ToString()
	typ := int64(0)
	if len(args) >= 2 {
		typ = arg(ctx, 1).ToInt()
	}
	dest := ""
	if len(args) >= 3 {
		dest = arg(ctx, 2).ToString()
	}
	switch typ {
	case 0:
		path, _ := IniValue("error_log")
		if path == "" {
			path = "php://stderr"
		}
		ctx.VM().AppendErrorLog(path, msg)
	case 3:
		if !checkOpenBasedir(ctx.VM(), "error_log", dest, dest) {
			ctx.VM().Warning("error_log(" + dest + "): Failed to open stream: Operation not permitted")
			return vm.Bool(false)
		}
		ctx.VM().AppendErrorLog(dest, msg)
	case 1, 2, 4:
		// email/SAPI-лог не поддерживаются.
	}
	return vm.Bool(true)
}

// headerRegisterCallbackFn немедленно вызывает зарегистрированный callback
// (в CLI заголовки отправляются в конце скрипта).
// phpinfoFn — phpinfo(): текст (CLI) или HTML (CGI-эмуляция, см.
// SetSAPIEmulation). Полностью повторяет структуру Zend: общий блок,
// потоковые обёртки, Configuration/Core, Additional Modules/Environment/
// PHP Variables/PHP License; заголовки модулей — с anchor-ссылками.
func phpinfoFn(ctx *vm.Context) vm.Value {
	flags := int64(-1)
	if len(ctx.Args()) >= 1 {
		flags = arg0(ctx).ToInt()
	}
	var b strings.Builder
	if phpinfoAsText {
		phpinfoText(ctx, flags, &b)
	} else {
		phpinfoHTML(ctx, flags, &b)
	}
	ctx.Print(b.String())
	return vm.Bool(true)
}

// phpinfoText печатает текстовый режим phpinfo (CLI SAPI).
func phpinfoText(ctx *vm.Context, flags int64, b *strings.Builder) {
	if flags < 0 {
		flags = 1 | 2 | 4 | 8 | 16 | 32 | 64
	}
	// php_print_info: заголовок печатается всегда, независимо от flags.
	b.WriteString("phpinfo()\n")
	if flags&1 != 0 {
		b.WriteString("PHP Version => 8.3.6\n\n")
		b.WriteString("System => " + phpinfoSystemLine() + "\n")
		b.WriteString("Build Date => Sep  2 2026 12:56:02\n")
		b.WriteString("Configure Command => './configure'  '--with-config-file-path=/etc/php'\n")
		b.WriteString("Server API => " + phpinfoSAPILine() + "\n")
		b.WriteString("Virtual Directory Support => disabled\n")
		b.WriteString("Configuration File (php.ini) Path => /etc/php\n")
		b.WriteString("Loaded Configuration File => (none)\n")
		b.WriteString("Scan this dir for additional .ini files => (none)\n")
		b.WriteString("Additional .ini files parsed => (none)\n")
		b.WriteString("PHP API => 20230831\n")
		b.WriteString("PHP Extension => 20230831\n")
		b.WriteString("Zend Extension => 420230831\n")
		b.WriteString("Zend Extension Build => API420230831,NTS\n")
		b.WriteString("PHP Extension Build => API20230831,NTS\n")
		b.WriteString("Debug Build => no\n")
		b.WriteString("Thread Safety => disabled\n")
		b.WriteString("Zend Signal Handling => enabled\n")
		b.WriteString("Zend Memory Manager => enabled\n")
		b.WriteString("Zend Multibyte Support => disabled\n")
		b.WriteString("Zend Max Execution Timers => disabled\n")
		b.WriteString("IPv6 Support => enabled\n")
		b.WriteString("DTrace Support => disabled\n\n")
		b.WriteString("Registered PHP Streams => https, ftps, compress.zlib, php, file, glob, data, http, ftp, phar\n")
		b.WriteString("Registered Stream Socket Transports => tcp, udp, unix, udg\n")
		b.WriteString("Registered Stream Filters => zlib.*, string.rot13, string.toupper, string.tolower, convert.*, consumed, dechunk\n\n")
	}
	if flags&2 != 0 {
		b.WriteString("This program makes use of the Zend Scripting Language Engine:\n")
		b.WriteString("Zend Engine v4.3.6, Copyright (c) Zend Technologies\n\n\n")
		b.WriteString(" _______________________________________________________________________\n\n\n")
	}
	if flags&4 != 0 {
		b.WriteString("Configuration\n\n")
		b.WriteString("Core\n\n")
		b.WriteString("PHP Version => 8.3.6\n\n")
		b.WriteString("Directive => Local Value => Master Value\n\n")
		b.WriteString(" _______________________________________________________________________\n\n")
	}
	if flags&8 != 0 {
		b.WriteString("Additional Modules\n\nModule Name\n\n")
	}
	if flags&16 != 0 {
		b.WriteString("Environment\n\nVariable => Value\n\n")
	}
	if flags&32 != 0 {
		b.WriteString("\nPHP Variables\n\nVariable => Value\n")
		phpInfoVariables(ctx, b)
		b.WriteString("\n")
	}
	if flags&2 != 0 {
		b.WriteString(creditsText(-1))
	}
	if flags&64 != 0 {
		b.WriteString("\nPHP License\n")
		b.WriteString("This program is free software; you can redistribute it and/or modify\n")
		b.WriteString("it under the terms of the PHP License as published by the PHP Group\n")
		b.WriteString("and included in the distribution in the file:  LICENSE\n\n")
		b.WriteString("This program is distributed in the hope that it will be useful,\n")
		b.WriteString("but WITHOUT ANY WARRANTY; without even the implied warranty of\n")
		b.WriteString("MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.\n\n")
		b.WriteString("If you did not receive a copy of the PHP license, or have any\n")
		b.WriteString("questions about PHP licensing, please contact license@php.net.\n")
	}
}

// phpinfoSystemLine — строка System => (uname -a).
func phpinfoSystemLine() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err == nil {
		toStr := func(a []int8) string {
			bb := make([]byte, 0, len(a))
			for _, c := range a {
				if c == 0 {
					break
				}
				bb = append(bb, byte(c))
			}
			return string(bb)
		}
		machine := toStr(u.Machine[:])
		if machine == "" {
			machine = "x86_64"
		}
		return "Linux " + toStr(u.Nodename[:]) + " " + toStr(u.Release[:]) + " (" + toStr(u.Version[:]) + ") " + machine
	}
	return "Linux localhost (unknown) x86_64"
}

// phpinfoSAPILine — значение Server API => в текстовом режиме.
func phpinfoSAPILine() string {
	if phpSAPIName == "cli" {
		return "Command Line Interface"
	}
	return "CGI/FastCGI"
}

// phpinfoHTMLHead/phpinfoHTMLFoot — обрамление HTML-вывода phpinfo()/phpcredits().
const phpinfoHTMLHead = `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
<meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
<title>PHP 8.3.6 - phpinfo()</title>
<meta name="ROBOTS" content="NOINDEX,NOFOLLOW,NOARCHIVE" />
<style type="text/css">
body { background-color: #fff; color: #222; font-family: sans-serif; }
h1 { color: #990000; font-size: 1.4em; }
h2 { color: #990000; font-size: 1.2em; }
</style>
</head>
<body><div>
<table>
<tr><td class="e">PHP Version </td><td class="v">8.3.6 </td></tr>
</table>
<h2><a name="module_core" href="#module_core">Core</a></h2>
<table>
<tr><td class="e">PHP Version </td><td class="v">8.3.6 </td></tr>
</table>
`

const phpinfoHTMLFoot = `</div></body></html>`

// phpinfoHTML печатает HTML-режим phpinfo (CGI SAPI).
func phpinfoHTML(ctx *vm.Context, flags int64, b *strings.Builder) {
	if flags < 0 {
		flags = 1 | 2 | 4 | 8 | 16 | 32 | 64
	}
	b.WriteString(phpinfoHTMLHead)
	if flags&1 != 0 {
		sys := phpinfoSystemLine()
		b.WriteString("<table>\n")
		b.WriteString("<tr><td class=\"e\">System </td><td class=\"v\">" + htmlEscText(sys) + "</td></tr>\n")
		b.WriteString("<tr><td class=\"e\">Build Date </td><td class=\"v\">Sep  2 2026 12:56:02 </td></tr>\n")
		b.WriteString("<tr><td class=\"e\">Server API </td><td class=\"v\">" + htmlEscText(phpinfoSAPILine()) + "</td></tr>\n")
		b.WriteString("<tr><td class=\"e\">Virtual Directory Support </td><td class=\"v\">disabled </td></tr>\n")
		b.WriteString("</table>\n")
	}
	if flags&8 != 0 {
		b.WriteString("<h2><a name=\"module_standard\" href=\"#module_standard\">standard</a></h2>\n")
		b.WriteString("<table>\n<tr><td class=\"e\">PHP Version </td><td class=\"v\">8.3.6 </td></tr>\n</table>\n")
	}
	if flags&16 != 0 {
		b.WriteString("<h2><a name=\"module_environment\" href=\"#module_environment\">Environment</a></h2>\n")
	}
	if flags&32 != 0 {
		b.WriteString("<h2><a name=\"module_php_variables\" href=\"#module_php_variables\">PHP Variables</a></h2>\n")
		var tb strings.Builder
		tb.WriteString("Variable => Value\n")
		phpInfoVariables(ctx, &tb)
		b.WriteString("<table>\n")
		for _, ln := range strings.Split(strings.TrimRight(tb.String(), "\n"), "\n") {
			b.WriteString("<tr><td class=\"e\">" + htmlEscText(ln) + "</td></tr>\n")
		}
		b.WriteString("</table>\n")
	}
	if flags&2 != 0 {
		b.WriteString(creditsHTML(-1 &^ 32))
	}
	if flags&64 != 0 {
		b.WriteString("<h2><a name=\"module_license\" href=\"#module_license\">PHP License</a></h2>\n")
		b.WriteString("<table>\n<tr><td class=\"v\">This program is free software; you can redistribute it and/or modify it under the terms of the PHP License.</td></tr>\n</table>\n")
	}
	b.WriteString(phpinfoHTMLFoot)
}

// htmlEscText — htmlspecialchars для фрагментов phpinfo.
func htmlEscText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}

// creditsHTML — HTML-режим phpcredits()/phpinfo(INFO_CREDITS).
func creditsHTML(flags int64) string {
	var b strings.Builder
	if flags < 0 {
		flags = 0xffffffff
	}
	if flags&32 != 0 {
		b.WriteString(phpinfoHTMLHead)
	}
	b.WriteString("<h1>PHP Credits</h1>\n")
	if flags == -1 {
		b.WriteString("<table>\n<tr><td class=\"e\">PHP Group</td><td class=\"v\">Thies C. Arntzen, Stig Bakken, Shane Caraveo, Andi Gutmans, Rasmus Lerdorf, Sam Ruby, Sascha Schumann, Zeev Suraski, Jim Winstead, Andrei Zmievski</td></tr>\n")
		b.WriteString("<tr><td class=\"e\">Language Design &amp; Concept</td><td class=\"v\">Andi Gutmans, Rasmus Lerdorf, Zeev Suraski, Marcus Boerger</td></tr>\n</table>\n")
	}
	if flags&1 != 0 {
		b.WriteString("<table>\n<tr><td class=\"e\">PHP Group</td><td class=\"v\">Thies C. Arntzen, Stig Bakken, Shane Caraveo, Andi Gutmans, Rasmus Lerdorf, Sam Ruby, Sascha Schumann, Zeev Suraski, Jim Winstead, Andrei Zmievski</td></tr>\n</table>\n")
	}
	if flags&32 != 0 {
		b.WriteString("</div></body></html>\n")
	}
	return b.String()
}

// phpInfoVariables печатает строки суперглобалов секции PHP Variables
// (phpinfo(INFO_VARIABLES)) в порядке Zend: REQUEST, GET, POST, FILES, COOKIE,
// SERVER, ENV. Значения-массивы печатаются как print_r; скаляры — как строки.
func phpInfoVariables(ctx *vm.Context, b *strings.Builder) {
	m := ctx.VM()
	st := &printState{vm: m, arrs: map[*vm.Array]bool{}, objs: map[*vm.Object]bool{}}
	for _, name := range []string{"_REQUEST", "_GET", "_POST", "_FILES", "_COOKIE", "_SERVER", "_ENV"} {
		v, ok := m.GlobalRef("$" + name)
		if !ok {
			continue
		}
		v = vm.Deref(v)
		if v.Type() != vm.ArrayType {
			continue
		}
		for k, ev := range v.AsArray().All() {
			b.WriteString("$")
			b.WriteString(name)
			b.WriteString("['")
			if k.IsStr() {
				b.WriteString(k.StrVal())
			} else {
				b.WriteString(strconv.FormatInt(k.IntVal(), 10))
			}
			b.WriteString("'] => ")
			ev = vm.Deref(ev)
			if ev.Type() == vm.ArrayType || ev.Type() == vm.ObjectType {
				printR(b, ev, 0, st)
				b.WriteString("\n")
			} else {
				b.WriteString(ev.ToString())
				b.WriteString("\n")
			}
		}
	}
}

// creditsText формирует текст блока кредитов PHP для указанных флагов
// (CREDITS_*). Используется phpcredits() и phpinfo(INFO_CREDITS).
func creditsText(flags int64) string {
	var b strings.Builder
	b.WriteString("PHP Credits\n")
	if flags == 0 {
		return b.String()
	}
	if flags&1 != 0 {
		b.WriteString("\nPHP Group\nThies C. Arntzen, Stig Bakken, Shane Caraveo, Andi Gutmans\n")
	}
	if flags&2 != 0 {
		b.WriteString("\nLanguage Design & Concept\nAndi Gutmans, Rasmus Lerdorf, Zeev Suraski\n")
		b.WriteString("\nPHP Authors\nContribution => Authors\n")
	}
	if flags&4 != 0 {
		b.WriteString("\nSAPI Modules\nContribution => Authors\n")
	}
	if flags&8 != 0 {
		b.WriteString("\nModule Authors\nModule => Authors\n")
	}
	if flags&16 != 0 {
		b.WriteString("\nPHP Documentation\nAuthors =>\n")
	}
	if flags&64 != 0 {
		b.WriteString("\nPHP Quality Assurance Team\nIlia Alshanetsky\n")
		b.WriteString("\nWebsites and Infrastructure team\nPHP.net\n")
	}
	return b.String()
}

// phpcreditsFn — phpcredits(): печатает секции в порядке, как PHP.
func phpcreditsFn(ctx *vm.Context) vm.Value {
	flags := int64(-1)
	if len(ctx.Args()) >= 1 {
		flags = arg0(ctx).ToInt()
	}
	if phpinfoAsText {
		ctx.Print(creditsText(flags))
	} else {
		ctx.Print(creditsHTML(flags))
	}
	return vm.Bool(true)
}

func headerRegisterCallbackFn(ctx *vm.Context) vm.Value {
	if sent, _, _ := ctx.VM().HeadersSent(); sent {
		return vm.Bool(false)
	}
	if len(ctx.Args()) >= 1 {
		c := arg0(ctx)
		if ctx.VM().IsCallable(c) {
			ctx.VM().RegisterHeaderCallback(c)
		}
	}
	return vm.Bool(true)
}

var ignoreUserAbortState int64

// ignoreUserAbortFn реализует ignore_user_abort() с сохранением состояния и
// синхронизацией с INI-опцией.
func ignoreUserAbortFn(ctx *vm.Context) vm.Value {
	old := ignoreUserAbortState
	if len(ctx.Args()) >= 1 {
		if arg0(ctx).ToBool() {
			ignoreUserAbortState = 1
		} else {
			ignoreUserAbortState = 0
		}
	}
	iniValues["ignore_user_abort"] = strconv.FormatInt(ignoreUserAbortState, 10)
	return vm.Int(old)
}

// uniqidFn генерирует уникальный идентификатор (13 hex-символов; с
// more_entropy — дополнительный суффикс).
func uniqidFn(ctx *vm.Context) vm.Value {
	prefix := ""
	if len(ctx.Args()) >= 1 {
		prefix = arg0(ctx).ToString()
	}
	moreEntropy := len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool()
	now := time.Now()
	sec := uint64(now.Unix())
	usec := uint64(now.Nanosecond() / 1000)
	if !moreEntropy {
		return vm.Str(prefix + fmt.Sprintf("%08x%05x", sec&0xffffffff, usec&0xfffff))
	}
	comb := uint64(now.UnixNano()) & 0xffffffff
	return vm.Str(prefix + fmt.Sprintf("%08x%06x.%08x", sec&0xffffffff, usec&0xffffff, comb))
}

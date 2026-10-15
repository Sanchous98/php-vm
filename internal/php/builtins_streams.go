package php

import (
	"io"
	"phpvm/internal/vm"
	"strings"
)

// Файл содержит встроенные функции потоков (stream_*).

// --- stream ---

func streamGetContentsFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_get_contents")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !h.ensureRead(ctx, "stream_get_contents") {
		return vm.Str("")
	}
	maxLen := -1
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() != vm.NullType {
		maxLen = int(arg(ctx, 1).ToInt())
		if maxLen < -1 {
			ctx.Throw(vm.NewThrowable("ValueError", "stream_get_contents(): Argument #2 ($length) must be greater than or equal to -1"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
	}
	// offset = -1 (по умолчанию) — не менять позицию; иначе Seeker в PHP
	// всегда позиционирует, в том числе на 0.
	offset := -1
	if len(ctx.Args()) >= 3 && arg(ctx, 2).Type() != vm.NullType {
		offset = int(arg(ctx, 2).ToInt())
	}
	if offset != -1 {
		h.push = -1
		h.Seek(int64(offset), io.SeekStart)
		h.eof = false
	}
	var data []byte
	if h.push >= 0 {
		data = append(data, byte(h.push))
		h.push = -1
	}
	if maxLen >= 0 {
		buf := make([]byte, maxLen)
		n, _ := h.Read(buf)
		data = append(data, buf[:n]...)
	} else {
		rest, _ := io.ReadAll(h)
		data = append(data, rest...)
		h.eof = true
	}
	return vm.Str(string(data))
}

func streamCopyToStreamFn(ctx *vm.Context) vm.Value {
	src := streamArg(ctx, 0, "stream_copy_to_stream")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	dst := streamArg(ctx, 1, "stream_copy_to_stream")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	length := int64(-1)
	if len(ctx.Args()) >= 3 {
		length = arg(ctx, 2).ToInt()
	}
	if length < 0 {
		n, err := io.Copy(dst, src)
		if err != nil {
			return vm.Bool(false)
		}
		return vm.Int(n)
	}
	n, err := io.CopyN(dst, src, length)
	if err != nil && err != io.EOF {
		return vm.Bool(false)
	}
	return vm.Int(n)
}

func streamGetLineFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_get_line")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	length := -1
	if len(ctx.Args()) >= 2 {
		l := int(arg(ctx, 1).ToInt())
		if l > 0 {
			length = l
		}
	}
	ending := ""
	if len(ctx.Args()) >= 3 {
		ending = arg(ctx, 2).ToString()
	}
	var out []byte
	readAny := false
	if ending == "" {
		for length < 0 || len(out) < length {
			b, ok := h.readByte()
			if !ok {
				break
			}
			readAny = true
			out = append(out, b)
		}
		if !readAny {
			return vm.Bool(false)
		}
		return vm.Str(string(out))
	}
	for {
		if length >= 0 && len(out) >= length {
			break
		}
		b, ok := h.readByte()
		if !ok {
			break
		}
		readAny = true
		out = append(out, b)
		if len(ending) > 0 && len(out) >= len(ending) &&
			string(out[len(out)-len(ending):]) == ending {
			out = out[:len(out)-len(ending)]
			break
		}
	}
	if !readAny {
		return vm.Bool(false)
	}
	return vm.Str(string(out))
}

// streamIsattyFn — stream_isatty(): сообщает, является ли поток терминалом.
// Вывод в тестах захвачен (не TTY), пользовательские обёртки не поддерживают
// cast в fd — всегда false (как в Zend при отсутствии stream_cast/fileno).
func streamIsattyFn(ctx *vm.Context) vm.Value {
	return vm.MakeBool(false)
}

func streamGetMetaDataFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_get_meta_data")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	// RFC2397: собственный набор полей (без timed_out/blocked/eof).
	if strings.HasPrefix(h.path, "data:") {
		r := parseDataURI(h.path)
		out := vm.NewArray()
		if r.mediaType != "" {
			out.SetStr("mediatype", vm.MakeString(r.mediaType))
		}
		for _, p := range r.params {
			val := p[1]
			switch p[0] {
			case "uri":
				val = h.path
			case "mediatype":
				val = r.mediaType
			}
			out.SetStr(p[0], vm.MakeString(val))
		}
		out.SetStr("base64", vm.MakeBool(r.base64))
		out.SetStr("wrapper_type", vm.MakeString("RFC2397"))
		out.SetStr("stream_type", vm.MakeString("RFC2397"))
		out.SetStr("mode", vm.MakeString(h.mode))
		out.SetStr("unread_bytes", vm.MakeInt(int64(unreadBytes(h))))
		out.SetStr("seekable", vm.MakeBool(true))
		out.SetStr("uri", vm.MakeString(h.path))
		return vm.MakeArray(out)
	}
	if h.isSocket {
		out := vm.NewArray()
		out.SetStr("timed_out", vm.MakeBool(h.timedOut))
		out.SetStr("blocked", vm.MakeBool(h.blockMode))
		out.SetStr("eof", vm.MakeBool(h.rbof && len(h.rbuf) == 0))
		streamType := h.sockType
		if streamType == "" {
			streamType = "tcp_socket"
		}
		out.SetStr("stream_type", vm.MakeString(streamType))
		out.SetStr("mode", vm.MakeString(h.mode))
		out.SetStr("unread_bytes", vm.MakeInt(int64(len(h.rbuf))))
		out.SetStr("seekable", vm.MakeBool(false))
		out.SetStr("uri", vm.MakeString(h.path))
		return vm.MakeArray(out)
	}
	// popen-поток: STDIO без обёртки (нет wrapper_type/uri), не seekable.
	if h.popenProc != nil {
		out := vm.NewArray()
		out.SetStr("timed_out", vm.MakeBool(false))
		out.SetStr("blocked", vm.MakeBool(true))
		out.SetStr("eof", vm.MakeBool(h.eof))
		out.SetStr("stream_type", vm.MakeString("STDIO"))
		out.SetStr("mode", vm.MakeString(h.mode))
		out.SetStr("unread_bytes", vm.MakeInt(int64(unreadBytes(h))))
		out.SetStr("seekable", vm.MakeBool(false))
		return vm.MakeArray(out)
	}
	out := vm.NewArray()
	wrapper, streamType := "plainfile", "STDIO"
	seekable := true
	switch {
	case h.dir:
		streamType = "dir"
	case strings.HasPrefix(h.path, "data:"):
		wrapper, streamType = "RFC2397", "RFC2397"
	case strings.HasPrefix(h.path, "php://"):
		wrapper, streamType = "PHP", "MEMORY"
	}
	// Порядок ключей совпадает с Zend.
	out.SetStr("timed_out", vm.MakeBool(false))
	out.SetStr("blocked", vm.MakeBool(true))
	out.SetStr("eof", vm.MakeBool(h.eof))
	out.SetStr("wrapper_type", vm.MakeString(wrapper))
	out.SetStr("stream_type", vm.MakeString(streamType))
	out.SetStr("mode", vm.MakeString(h.mode))
	out.SetStr("unread_bytes", vm.MakeInt(int64(unreadBytes(h))))
	out.SetStr("seekable", vm.MakeBool(seekable))
	if !h.dir {
		u := h.path
		if h.uri != "" {
			u = h.uri
		}
		out.SetStr("uri", vm.MakeString(u))
	}
	// Дополнительные поля обёртки кладутся после стандартных.
	if len(h.meta) > 0 {
		std := map[string]bool{"timed_out": true, "blocked": true, "eof": true, "wrapper_type": true, "stream_type": true, "mode": true, "unread_bytes": true, "seekable": true, "uri": true}
		for k, v := range h.meta {
			if !std[k] {
				out.SetStr(k, v)
			}
		}
	}
	return vm.MakeArray(out)
}

// unreadBytes — число байт во внутреннем буфере потока (pushback).
func unreadBytes(h *fsHandle) int {
	if h.push >= 0 {
		return 1
	}
	return 0
}

// streamSetChunkSizeFn — stream_set_chunk_size(): изменяет размер буфера.
func streamSetChunkSizeFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_set_chunk_size")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	size := arg(ctx, 1).ToInt()
	if size <= 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "stream_set_chunk_size(): Argument #2 ($size) must be greater than 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	prev := int64(h.chunk())
	h.chunkSize = int(size)
	return vm.Int(prev)
}

// --- getopt ---

func getoptFn(ctx *vm.Context) vm.Value {
	shortopts := arg0(ctx).ToString()
	var longopts []string
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() == vm.ArrayType {
		for _, v := range arg(ctx, 1).AsArray().Values() {
			longopts = append(longopts, v.ToString())
		}
	}
	var argv []string
	hasArgv := false
	if v, ok := ctx.VM().GlobalGet("$argv"); ok && v.Type() == vm.ArrayType {
		hasArgv = true
		for _, a := range v.AsArray().Values() {
			argv = append(argv, a.ToString())
		}
	}
	out := vm.NewArray()
	if !hasArgv || len(argv) < 1 {
		return vm.Bool(false)
	}
	setOpt := func(key string, val vm.Slot) {
		var k vm.ArrayKey
		if n, ok := canonicalDecimalKey(key); ok {
			k = vm.MakeIntKey(n)
		} else {
			k = vm.MakeStrKey(key)
		}
		if ex, ok := out.Get(k); ok {
			var arr *vm.Array
			if ex.Type() == vm.ArrayType {
				arr = ex.AsArray()
			} else {
				arr = vm.NewArray()
				arr.Append(ex)
			}
			arr.Append(val)
			out.Set(k, vm.MakeArray(arr))
		} else {
			out.Set(k, val)
		}
	}
	// shortSpec: карта опция -> 0 (нет арг), 1 (обязательный), 2 (опциональный).
	shortSpec := map[rune]int{}
	runes := []rune(shortopts)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == ':' {
			continue
		}
		need := 0
		if i+1 < len(runes) && runes[i+1] == ':' {
			need = 1
			if i+2 < len(runes) && runes[i+2] == ':' {
				need = 2
			}
		}
		shortSpec[c] = need
	}
	longSpec := map[string]int{}
	for _, lo := range longopts {
		need := 0
		name := lo
		if strings.HasSuffix(lo, "::") {
			need = 2
			name = strings.TrimSuffix(lo, "::")
		} else if strings.HasSuffix(lo, ":") {
			need = 1
			name = strings.TrimSuffix(lo, ":")
		}
		longSpec[name] = need
	}
	i := 1
	for ; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			i++
			break
		}
		if strings.HasPrefix(a, "--") {
			body := a[2:]
			name := body
			val := ""
			hasVal := false
			if eq := strings.IndexByte(body, '='); eq >= 0 {
				name = body[:eq]
				val = body[eq+1:]
				hasVal = true
			}
			need, ok := longSpec[name]
			if !ok {
				// Неизвестная опция: PHP возвращает false для всего вызова.
				return vm.Bool(false)
			}
			switch {
			case hasVal:
				setOpt(name, vm.MakeString(val))
			case need == 0:
				setOpt(name, vm.MakeBool(false))
			case need == 1:
				if i+1 >= len(argv) {
					return vm.MakeArray(out)
				}
				i++
				setOpt(name, vm.MakeString(argv[i]))
			default:
				setOpt(name, vm.MakeBool(false))
			}
			continue
		}
		if len(a) >= 2 && a[0] == '-' {
			chars := []rune(a[1:])
			for ci := 0; ci < len(chars); ci++ {
				c := chars[ci]
				need, ok := shortSpec[c]
				if !ok {
					return vm.Bool(false)
				}
				if need == 0 {
					setOpt(string(c), vm.MakeBool(false))
					continue
				}
				if ci+1 < len(chars) {
					rest := string(chars[ci+1:])
					rest = strings.TrimPrefix(rest, "=")
					setOpt(string(c), vm.MakeString(rest))
					ci = len(chars)
				} else {
					if need == 1 {
						if i+1 >= len(argv) {
							return vm.MakeArray(out)
						}
						i++
						setOpt(string(c), vm.MakeString(argv[i]))
					} else {
						setOpt(string(c), vm.MakeBool(false))
					}
				}
			}
			continue
		}
		break
	}
	if len(ctx.Args()) >= 3 && arg(ctx, 2).IsRef() {
		if r := arg(ctx, 2).Ref(); r != nil {
			r.Set(vm.MakeInt(int64(i)))
		}
	}
	return vm.MakeArray(out)
}
func zendLeakVariableFn(ctx *vm.Context) vm.Value {
	return vm.MakeNull()
}

// --- прочие базовые функции ---

var sessionIDValue = ""

func sessionIDFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) >= 1 && arg0(ctx).Type() != vm.NullType {
		old := sessionIDValue
		sessionIDValue = arg0(ctx).ToString()
		return vm.Str(old)
	}
	return vm.Str(sessionIDValue)
}

func isIterableFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx)
	if s.Type() == vm.ArrayType {
		return vm.Bool(true)
	}
	if s.Type() == vm.ObjectType {
		return vm.Bool(ctx.VM().InstanceOfName(s, "Traversable"))
	}
	return vm.Bool(false)
}

func iniRestoreFn(ctx *vm.Context) vm.Value {
	name := normalizeIniKey(arg0(ctx).ToString())
	if _, ok := iniValues[name]; ok {
		if orig, ok2 := iniOriginal[name]; ok2 {
			iniValues[name] = orig
		} else {
			iniValues[name] = defaultIniValues()[name]
		}
		if name == "include_path" {
			fsIncludePath = iniValues[name]
		}
	}
	return vm.MakeNull()
}

// setTypeFn — settype(): приводит переменную к указанному типу (by-ref).
func setTypeFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "settype() expects exactly 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	typ := strings.ToLower(arg(ctx, 1).ToString())
	raw := arg0(ctx)
	cur := derefSlot(raw)
	var v vm.Slot
	switch typ {
	case "int", "integer":
		v = vm.MakeInt(zvalGetLong(ctx, cur))
	case "float", "double":
		v = vm.MakeFloat(zvalGetDouble(ctx, cur))
	case "string":
		out, err := ctx.VM().ToStringValue(cur)
		if err != nil {
			// PHP приводит zval к "" и лишь затем бросает Error.
			if raw.IsRef() {
				if r := raw.Ref(); r != nil {
					r.Set(vm.MakeString(""))
				}
			}
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		v = vm.MakeString(out)
	case "bool", "boolean":
		v = vm.MakeBool(cur.ToBool())
	case "array":
		v = ctx.VM().Cast(cur, vm.CastArray)
	case "object":
		v = ctx.VM().CastToObject(cur)
	case "null":
		v = vm.MakeNull()
	case "resource":
		ctx.Throw(vm.NewThrowable("ValueError", "Cannot convert to resource type"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	default:
		ctx.Throw(vm.NewThrowable("ValueError", "settype(): Argument #2 ($type) must be a valid type"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if raw.IsRef() {
		if r := raw.Ref(); r != nil {
			throwCallable(ctx, ctx.VM().SetRefChecked(r, v))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	return vm.Bool(true)
}

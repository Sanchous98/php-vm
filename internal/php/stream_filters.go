package php

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"strings"

	"phpvm/internal/vm"
)

// Фильтры потока: встроенные (string.*, convert.*) и пользовательские
// (php_user_filter + stream_filter_register/append/prepend/remove).

// streamFilter — один фильтр, прикреплённый к потоку.
type streamFilter struct {
	name   string
	params vm.Slot
	read   bool
	write  bool

	// Встроенный фильтр: преобразование буфера целиком.
	builtin func([]byte) []byte

	// Пользовательский фильтр.
	user     vm.Slot
	userVM   *vm.VM
	userCtx  *vm.Context
	userCls  string
	onCreate bool
	// closeCalled — завершающий filter(closing=true) уже выполнен (явным
	// закрытием/удалением). eofClosing — closing уже приходил при чтении до
	// конца потока: явный fclose повторный вызов не делает (bug79984), а
	// уничтожение ресурса в конце запроса делает (gh20370).
	closeCalled bool
	eofClosing  bool
	// streamPropErr — ошибка присваивания $this->stream (GH-20370:
	// типизированное свойство не принимает resource): всплывает при первой
	// операции с потоком (fwrite/fclose) после предупреждения о необработанной
	// input-бригаде.
	streamPropErr error
	initDone      bool

	// owner/res — поток-владелец и ресурс-обёртка фильтра (для remove).
	owner *fsHandle
	res   vm.Slot

	// closed — filter() уже получил $closing=true; onClosed — onClose вызван.
	closed   bool
	onClosed bool
	// fatal — filter() вернул PSFS_ERR_FATAL.
	fatal bool
}

// closeFilter завершает фильтр: вызов filter($closing=true) и onClose().
// Идемпотентно; при closing, уже полученном на EOF, явное закрытие
// повторного вызова не делает (bug79984).
func (f *streamFilter) closeFilter() { f.closeFilterMode(false) }

// closeFilterRequestEnd — завершение фильтра при уничтожении ресурса в конце
// запроса: closing вызывается даже после EOF-чтения (gh20370).
func (f *streamFilter) closeFilterRequestEnd() { f.closeFilterMode(true) }

func (f *streamFilter) closeFilterMode(requestEnd bool) {
	if f.builtin != nil {
		return
	}
	if !f.ensureInit() {
		return
	}
	if !f.closeCalled && (requestEnd || !f.eofClosing) {
		f.closeCalled = true
		f.applyUser(nil, true)
	}
	f.closeCalled = true
	if f.onClosed {
		return
	}
	f.onClosed = true
	if o := f.userVM.SlotObject(f.user); o != nil {
		if ci := f.userVM.FindClass(o.ClassName()); ci != nil {
			for _, m := range []string{"onclose", "onClose"} {
				if _, ok := ci.MethodFunction(m); ok {
					arr := vm.NewArray()
					arr.Append(f.user)
					arr.Append(vm.MakeString(m))
					_, _ = f.userVM.CallCallable(f.userCtx, vm.MakeArray(arr), nil)
					break
				}
			}
		}
	}
}

// closeFilters завершает все фильтры потока (при fclose).
func closeFilters(h *fsHandle) {
	for _, f := range h.filters {
		f.closeFilter()
	}
	h.filters = nil
}

// closeFiltersRequestEnd завершает фильтры при уничтожении ресурса в конце
// запроса (filter(closing=true) зовётся и после EOF-чтения — gh20370).
func closeFiltersRequestEnd(h *fsHandle) {
	for _, f := range h.filters {
		f.closeFilterRequestEnd()
	}
	h.filters = nil
}

// ensureInit лениво создаёт объект пользовательского фильтра и вызывает
// oncreate при первом применении. Ленивость нужна, чтобы вложенный запуск VM
// не происходил внутри нативной stream_filter_append (это ломало бы
// регистры вызывающего кадра).
func (f *streamFilter) ensureInit() bool {
	if f.initDone || f.userCls == "" {
		return true
	}
	f.initDone = true
	v := f.userVM
	ci := v.FindClass(f.userCls)
	if ci == nil {
		v.Autoload(f.userCtx, f.userCls)
		ci = v.FindClass(f.userCls)
	}
	if ci == nil {
		return false
	}
	obj, err := v.NewInstanceByNameFromInternal(f.userCls, nil)
	if err == nil {
		// Ленивый дефолт свойства (несуществующая константа) всплывает из
		// pending-очереди NewInstance (object_init_failure).
		if derr := v.TakePendingDefaultError(); derr != nil {
			err = derr
		}
	}
	if err != nil {
		throwCallable(f.userCtx, err)
		return false
	}
	f.user = obj
	if o := v.SlotObject(obj); o != nil {
		ci.SetProp(o, "params", f.params)
	}
	// Имя метода чувствительно к регистру, как и имена методов в PHP:
	// проверяем оба написания (onCreate — каноническое).
	for _, mn := range []string{"oncreate", "onCreate"} {
		if _, ok := ci.MethodFunction(mn); !ok {
			continue
		}
		arr := vm.NewArray()
		arr.Append(obj)
		arr.Append(vm.MakeString(mn))
		res, err := v.CallCallable(f.userCtx, vm.MakeArray(arr), nil)
		if err != nil {
			throwCallable(f.userCtx, err)
			return false
		}
		if !res.ToBool() {
			return false
		}
		break
	}
	// GH-20370: $this->stream присваивается ПОСЛЕ onCreate и только если
	// свойство объявлено (public/protected) или уже существует как
	// динамическое (создано в onCreate на #[AllowDynamicProperties]-классе);
	// private не трогаем, отсутствующее не создаём. Ошибка типизированного
	// свойства откладывается до первой операции (fwrite/fclose).
	if o := v.SlotObject(obj); o != nil {
		assign := false
		if vis, declared := v.PropVisibility(ci, "stream"); declared {
			assign = vis != vm.Private
		} else {
			assign = v.ObjectPropExists(obj, "stream", false)
		}
		if assign {
			var val vm.Slot
			if f.owner != nil && f.owner.resSlot.Type() == vm.ResourceType {
				val = f.owner.resSlot
			} else {
				val = vm.MakeNull()
			}
			if perr := v.SetPropertyChecked(obj, "stream", val, false); perr != nil {
				f.streamPropErr = perr
			}
		}
	}
	return true
}

// apply применяет фильтр к данным (для потока целиком).
func (f *streamFilter) apply(data []byte) []byte { return f.applyClosing(data, false) }

func (f *streamFilter) applyClosing(data []byte, closing bool) []byte {
	if f.builtin != nil {
		return f.builtin(data)
	}
	if !f.ensureInit() {
		return data
	}
	if f.owner != nil {
		f.owner.filterActive = true
		defer func() { f.owner.filterActive = false }()
	}
	return f.applyUser(data, closing)
}

// streamFilterRegistry — зарегистрированные пользовательские фильтры:
// имя -> класс.
var streamFilterRegistry = map[string]string{}

// builtinFilterFunc возвращает встроенный фильтр по имени, включая
// параметризованный convert.iconv.<from>/<to>. Для iconv преобразование
// применяется напрямую (см. iconvFilter).
func builtinFilterFunc(name string) (func([]byte) []byte, bool) {
	if fn, ok := builtinFilters[name]; ok {
		return fn, true
	}
	if strings.HasPrefix(name, "convert.iconv.") {
		enc := name[len("convert.iconv."):]
		return func(b []byte) []byte { return iconvFilterApply(enc, b) }, true
	}
	return nil, false
}

// iconvFilterApply применяет convert.iconv.<from>/<to> к данным.
func iconvFilterApply(enc string, b []byte) []byte {
	i := strings.IndexByte(enc, '/')
	if i <= 0 {
		return b
	}
	from, to := enc[:i], enc[i+1:]
	return iconvBytes(from, to, b)
}

// builtinFilters — встроенные фильтры.
var builtinFilters = map[string]func([]byte) []byte{
	"string.rot13": func(b []byte) []byte {
		out := make([]byte, len(b))
		for i, c := range b {
			switch {
			case c >= 'a' && c <= 'z':
				out[i] = 'a' + (c-'a'+13)%26
			case c >= 'A' && c <= 'Z':
				out[i] = 'A' + (c-'A'+13)%26
			default:
				out[i] = c
			}
		}
		return out
	},
	"string.toupper": func(b []byte) []byte { return []byte(strings.ToUpper(string(b))) },
	"string.tolower": func(b []byte) []byte { return []byte(strings.ToLower(string(b))) },
	"convert.base64-encode": func(b []byte) []byte {
		return []byte(base64.StdEncoding.EncodeToString(b))
	},
	"convert.base64-decode": func(b []byte) []byte {
		d, err := base64.StdEncoding.DecodeString(string(b))
		if err != nil {
			return b
		}
		return d
	},
	"string.strip_tags": func(b []byte) []byte { return []byte(stripTagsSimple(string(b))) },
	"convert.quoted-printable-encode": func(b []byte) []byte {
		var out []byte
		const hex = "0123456789ABCDEF"
		for _, c := range b {
			if c >= 0x20 && c <= 0x7e && c != '=' {
				out = append(out, c)
			} else {
				out = append(out, '=', hex[c>>4], hex[c&0xf])
			}
		}
		return out
	},
	"convert.quoted-printable-decode": func(b []byte) []byte {
		var out []byte
		for i := 0; i < len(b); i++ {
			if b[i] == '=' && i+2 < len(b) {
				hi := qpHex(b[i+1])
				lo := qpHex(b[i+2])
				if hi >= 0 && lo >= 0 {
					out = append(out, byte(hi<<4|lo))
					i += 2
					continue
				}
			}
			out = append(out, b[i])
		}
		return out
	},
	"bzip2.compress": func(b []byte) []byte { return b },
	"bzip2.decompress": func(b []byte) []byte {
		return b
	},
	"zlib.deflate": func(b []byte) []byte {
		var buf bytes.Buffer
		w, err := flate.NewWriter(&buf, flate.DefaultCompression)
		if err != nil {
			return b
		}
		w.Write(b)
		w.Close()
		return buf.Bytes()
	},
	"zlib.inflate": func(b []byte) []byte {
		r := flate.NewReader(bytes.NewReader(b))
		defer r.Close()
		out, err := io.ReadAll(r)
		if err != nil {
			return b
		}
		return out
	},
}

// newQPEncodeFilter строит фильтр quoted-printable-encode с учётом опций
// line-break-chars, line-length и binary (php_conv_qp_encode).
//
// Текстовый режим (перенос строк как в php_conv_qp_encode) включается только
// при заданном line-length и binary=false: тогда LF — мягкий перенос, CR
// отбрасывается, хвостовые пробелы кодируются. Без line-length (и при
// binary=true) CR/LF кодируются как =0D/=0A, а пробелы (в binary — и табы)
// обрабатываются как обычные символы.
func newQPEncodeFilter(params vm.Slot) func([]byte) []byte {
	lineBreak := "\n"
	lineLen := 0
	binary := false
	if params.Type() == vm.ArrayType {
		a := params.AsArray()
		if v, ok := a.Get(vm.MakeStrKey("line-break-chars")); ok && v.Type() != vm.NullType {
			lineBreak = v.ToString()
		}
		if v, ok := a.Get(vm.MakeStrKey("line-length")); ok && v.Type() != vm.NullType {
			lineLen = int(v.ToInt())
		}
		if v, ok := a.Get(vm.MakeStrKey("binary")); ok && v.Type() != vm.NullType {
			binary = v.ToBool()
		}
	}
	textMode := lineLen > 0 && !binary
	return func(b []byte) []byte {
		const hex = "0123456789ABCDEF"
		var out []byte
		cur := 0
		maxContent := lineLen - 1
		if maxContent < 1 {
			maxContent = 1
		}
		emit := func(s []byte) {
			if lineLen > 0 && cur+len(s) > maxContent {
				out = append(out, '=')
				out = append(out, lineBreak...)
				cur = 0
			}
			out = append(out, s...)
			cur += len(s)
		}
		for i := 0; i < len(b); i++ {
			c := b[i]
			if textMode && c == '\n' {
				out = append(out, lineBreak...)
				cur = 0
				continue
			}
			if textMode && c == '\r' {
				continue
			}
			if c == ' ' || c == '\t' {
				if binary {
					emit([]byte{'=', hex[c>>4], hex[c&0xf]})
					continue
				}
				if textMode {
					// Кодируется, если до конца строки больше нет
					// не-пробельных символов.
					trailing := true
					for j := i + 1; j < len(b); j++ {
						if b[j] == '\n' {
							break
						}
						if b[j] != ' ' && b[j] != '\t' {
							trailing = false
							break
						}
					}
					if trailing {
						emit([]byte{'=', hex[c>>4], hex[c&0xf]})
						continue
					}
				}
				emit([]byte{c})
				continue
			}
			if c >= 0x21 && c <= 0x7e && c != '=' {
				emit([]byte{c})
				continue
			}
			emit([]byte{'=', hex[c>>4], hex[c&0xf]})
		}
		return out
	}
}

// newBase64EncodeFilter строит фильтр base64-encode с учётом опций
// line-length и line-break-chars (php_stream_filter base64).
func newBase64EncodeFilter(params vm.Slot) func([]byte) []byte {
	lineLen := 0
	lineBreak := "\n"
	if params.Type() == vm.ArrayType {
		a := params.AsArray()
		if v, ok := a.Get(vm.MakeStrKey("line-length")); ok && v.Type() != vm.NullType {
			lineLen = int(v.ToInt())
		}
		if v, ok := a.Get(vm.MakeStrKey("line-break-chars")); ok && v.Type() != vm.NullType {
			lineBreak = v.ToString()
		}
	}
	return func(b []byte) []byte {
		s := base64.StdEncoding.EncodeToString(b)
		if lineLen <= 0 {
			return []byte(s)
		}
		var out []byte
		for len(s) > lineLen {
			out = append(out, s[:lineLen]...)
			out = append(out, lineBreak...)
			s = s[lineLen:]
		}
		return append(out, s...)
	}
}

// newQPDecodeFilter строит фильтр quoted-printable-decode: невалидная
// последовательность после '=' порождает Warning (php_conv_qp_decode).
func newQPDecodeFilter(v *vm.VM) func([]byte) []byte {
	return func(b []byte) []byte {
		var out []byte
		warned := false
		for i := 0; i < len(b); i++ {
			if b[i] == '=' && i+2 < len(b) {
				hi := qpHex(b[i+1])
				lo := qpHex(b[i+2])
				if hi >= 0 && lo >= 0 {
					out = append(out, byte(hi<<4|lo))
					i += 2
					continue
				}
				if !warned {
					v.Warning(v.CurrentFunctionName() + "(): Stream filter (convert.quoted-printable-decode): invalid byte sequence")
					warned = true
				}
			}
			out = append(out, b[i])
		}
		return out
	}
}

func qpHex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return -1
}

func stripTagsSimple(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		if s[i] == '<' {
			in = true
			continue
		}
		if s[i] == '>' && in {
			in = false
			continue
		}
		if !in {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// readFilters возвращает true, если к потоку привязаны читающие фильтры.
func (h *fsHandle) hasReadFilters() bool {
	if h.us != nil {
		return false
	}
	for _, f := range h.filters {
		if f.read {
			return true
		}
	}
	return false
}

func (h *fsHandle) hasWriteFilters() bool {
	for _, f := range h.filters {
		if f.write {
			return true
		}
	}
	return false
}

// applyWriteFilters применяет пишущие фильтры к данным.
func (h *fsHandle) applyWriteFilters(data []byte) []byte {
	for _, f := range h.filters {
		if f.write {
			data = f.apply(data)
		}
	}
	return data
}

// resetFilterState сбрасывает состояние буферизации читающих фильтров.
func (h *fsHandle) resetFilterState() {
	h.filterBuf = nil
	h.filterEOF = false
}

// filteredRead читает из потока, пропуская данные через читающие фильтры.
func (h *fsHandle) filteredRead(p []byte) (int, error) {
	if len(h.filterBuf) == 0 {
		if h.filterEOF {
			return 0, io.EOF
		}
		buf := make([]byte, 8192)
		n, err := h.f.Read(buf)
		eof := err != nil || n < len(buf)
		if eof {
			// feof($this->stream) внутри пользовательского фильтра должен
			// видеть конец потока: обновляем признак EOF у обёртки, иначе
			// чтение через фильтр его не выставляет.
			h.eof = true
		} else {
			h.eof = false
		}
		if n > 0 {
			h.rawConsumed += int64(n)
		}
		data := buf[:n]
		for _, f := range h.filters {
			if f.read {
				data = f.applyClosing(data, eof)
				if eof {
					f.closed = true
					f.eofClosing = true
				}
				if f.fatal {
					h.filterEOF = true
					return 0, errStreamReadFailed
				}
			}
		}
		if eof {
			h.filterEOF = true
		}
		h.filterBuf = data
		if len(h.filterBuf) == 0 {
			if eof {
				return 0, io.EOF
			}
			return 0, nil
		}
	}
	n := copy(p, h.filterBuf)
	h.filterBuf = h.filterBuf[n:]
	return n, nil
}

// --- пользовательские фильтры ---

type bucketBrigade struct {
	buckets []vm.Slot
	pos     int
}

// makeBucket создаёт объект-bucket (stdClass) с полями data/datalen.
func makeBucket(v *vm.VM, data string) vm.Slot {
	ci := v.FindClass("stdClass")
	if ci == nil {
		return vm.MakeNull()
	}
	slot, o := v.NewObjectOf(ci)
	ci.SetProp(o, "data", vm.MakeString(data))
	ci.SetProp(o, "datalen", vm.MakeInt(int64(len(data))))
	return slot
}

// newBrigade оборачивает список bucket-ов в ресурс.
func newBrigade(slots []vm.Slot) vm.Slot {
	id := fsNextID
	fsNextID++
	return vm.MakeResource(id, "bucket-brigade", &bucketBrigade{buckets: slots})
}

// bucketData извлекает поле data из bucket-объекта.
func bucketData(v *vm.VM, b vm.Slot) (string, bool) {
	o := v.SlotObject(b)
	if o == nil {
		return "", false
	}
	ci := v.FindClass(o.ClassName())
	if ci == nil {
		return "", false
	}
	if d, ok := ci.GetProp(o, "data"); ok {
		return d.ToString(), true
	}
	return "", false
}

// applyUser прогоняет данные через пользовательский фильтр.
func (f *streamFilter) applyUser(data []byte, closing bool) []byte {
	out, _ := f.applyUserCode(data, closing)
	return out
}

// applyUserCode — applyUser с кодом возврата filter() (PSFS_*): читающий
// php://filter путь различает PASS_ON (0x1) и FEED_ME (0) — при FEED_ME
// выходная бригада не отдаётся потребителю (oss_fuzz_385993744: "").
func (f *streamFilter) applyUserCode(data []byte, closing bool) ([]byte, int64) {
	if f.userVM == nil {
		return data, 1
	}
	v := f.userVM
	if f.streamPropErr != nil {
		// Zend: ошибка установки $this->stream прерывает фильтр до вызова
		// filter(); входная бригада остаётся необработанной (gh20370).
		if !closing {
			fn := v.NativeCallerName()
			if fn == "" {
				fn = "stream"
			}
			f.userCtx.VM().Warning(fn + "(): Unprocessed filter buckets remaining on input brigade")
		}
		throwCallable(f.userCtx, f.streamPropErr)
		return data, 1
	}
	var in vm.Slot
	if len(data) > 0 {
		in = newBrigade([]vm.Slot{makeBucket(v, string(data))})
	} else {
		in = newBrigade(nil)
	}
	out := newBrigade(nil)
	consumed := vm.NewRef(vm.MakeInt(0))
	arr := vm.NewArray()
	arr.Append(f.user)
	arr.Append(vm.MakeString("filter"))
	res, err := v.CallCallable(f.userCtx, vm.MakeArray(arr), []vm.Slot{
		in, out, consumed, vm.MakeBool(closing),
	})
	if err != nil {
		throwCallable(f.userCtx, err)
		return data, 1
	}
	if res.ToInt() == 2 { // PSFS_ERR_FATAL
		f.fatal = true
	}
	br, _ := vm.AsResource(out).Val.(*bucketBrigade)
	if br == nil {
		return data, res.ToInt()
	}
	var result strings.Builder
	for _, b := range br.buckets {
		if s, ok := bucketData(v, b); ok {
			result.WriteString(s)
		}
	}
	return []byte(result.String()), res.ToInt()
}

// --- функции ---

func streamFilterRegisterFn(ctx *vm.Context) vm.Value {
	name := arg0(ctx).ToString()
	class := arg(ctx, 1).ToString()
	if name == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "stream_filter_register(): Argument #1 ($filter_name) must be a non-empty string"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if class == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "stream_filter_register(): Argument #2 ($class) must be a non-empty string"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if _, ok := builtinFilters[name]; ok {
		// Zend возвращает false без диагностики (GH-17037).
		return vm.Bool(false)
	}
	if _, ok := streamFilterRegistry[name]; ok {
		return vm.Bool(false)
	}
	// Класс не проверяется: несуществующий класс даст ошибку лишь при
	// создании фильтра (Zend), регистрация успешна.
	streamFilterRegistry[name] = class
	return vm.Bool(true)
}

func streamFilterAppendFn(ctx *vm.Context) vm.Value  { return streamFilterAttach(ctx, false) }
func streamFilterPrependFn(ctx *vm.Context) vm.Value { return streamFilterAttach(ctx, true) }

func streamFilterAttach(ctx *vm.Context, prepend bool) vm.Value {
	h := streamArg(ctx, 0, "stream_filter_append")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	name := arg(ctx, 1).ToString()
	mode := int64(3)
	if len(ctx.Args()) >= 3 {
		mode = arg(ctx, 2).ToInt()
	}
	params := vm.MakeNull()
	if len(ctx.Args()) >= 4 {
		params = arg(ctx, 3)
	}
	f, ok := buildFilter(ctx, name, params, mode)
	if !ok {
		return vm.Bool(false)
	}
	id := fsNextID
	fsNextID++
	f.res = vm.MakeResource(id, "stream filter", f)
	f.owner = h
	// Пользовательский фильтр инстанцируется сразу: onCreate() может
	// отказать (false) — тогда append/prepend возвращает false с Warning.
	// $this->stream должен уже указывать на поток.
	if f.builtin == nil && !f.ensureInit() {
		ctx.VM().Warning("stream_filter_append(): Unable to create or locate filter \"" + name + "\"")
		return vm.Bool(false)
	}
	if prepend {
		h.filters = append([]*streamFilter{f}, h.filters...)
	} else {
		h.filters = append(h.filters, f)
	}
	h.resetFilterState()
	h.rawConsumed = 0
	return f.res
}

// buildFilter создаёт фильтр по имени (встроенный или пользовательский).
func buildFilter(ctx *vm.Context, name string, params vm.Slot, mode int64) (*streamFilter, bool) {
	read := mode&1 != 0
	write := mode&2 != 0
	if mode < 0 {
		read, write = true, true
	}
	f := &streamFilter{name: name, params: params, read: read, write: write}
	if name == "convert.quoted-printable-encode" {
		f.builtin = newQPEncodeFilter(params)
		return f, true
	}
	if name == "convert.base64-encode" {
		f.builtin = newBase64EncodeFilter(params)
		return f, true
	}
	if name == "convert.quoted-printable-decode" {
		f.builtin = newQPDecodeFilter(ctx.VMInstance())
		return f, true
	}
	if fn, ok := builtinFilters[name]; ok {
		f.builtin = fn
		return f, true
	}
	cls, ok := streamFilterRegistry[name]
	if !ok {
		ctx.VM().Warning("stream_filter_append(): Unable to find or instantiate the filter \"" + name + "\"")
		return nil, false
	}
	f.userCls = cls
	f.userVM = ctx.VMInstance()
	f.userCtx = ctx
	return f, true
}

func streamFilterRemoveFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg0(ctx))
	if r == nil || r.Kind != "stream filter" {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("stream_filter_remove", 1, "stream", "resource", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	f, _ := r.Val.(*streamFilter)
	if f == nil || f.owner == nil {
		return vm.Bool(false)
	}
	h := f.owner
	f.closeFilter()
	for i, x := range h.filters {
		if x == f {
			h.filters = append(h.filters[:i], h.filters[i+1:]...)
			break
		}
	}
	h.resetFilterState()
	r.Closed = true
	return vm.Bool(true)
}

func streamGetFiltersFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for name := range builtinFilters {
		out.Append(vm.MakeString(name))
	}
	for name := range streamFilterRegistry {
		out.Append(vm.MakeString(name))
	}
	return vm.MakeArray(out)
}

func streamBucketNewFn(ctx *vm.Context) vm.Value {
	return makeBucket(ctx.VMInstance(), arg(ctx, 1).ToString())
}

func streamBucketAppendFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg(ctx, 0))
	if r == nil {
		return vm.MakeNull()
	}
	br, _ := r.Val.(*bucketBrigade)
	if br == nil {
		return vm.MakeNull()
	}
	// Повторное добавление той же корзины в ту же бригаду игнорируется:
	// Zend помечает корзину владельцем-бригадой, и второй append не создаёт
	// дубликат (bug35916: ожидается один THANK YOU, а не два).
	b := arg(ctx, 1)
	for _, x := range br.buckets {
		if x == b {
			return vm.MakeNull()
		}
	}
	br.buckets = append(br.buckets, b)
	return vm.MakeNull()
}

// streamBucketPrependFn — stream_bucket_prepend(): вставляет корзину в начало.
func streamBucketPrependFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg(ctx, 0))
	if r == nil {
		return vm.MakeNull()
	}
	br, _ := r.Val.(*bucketBrigade)
	if br == nil {
		return vm.MakeNull()
	}
	br.buckets = append([]vm.Slot{arg(ctx, 1)}, br.buckets...)
	return vm.MakeNull()
}

func streamBucketMakeWriteableFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg(ctx, 0))
	if r == nil {
		return vm.MakeNull()
	}
	br, _ := r.Val.(*bucketBrigade)
	if br == nil || br.pos >= len(br.buckets) {
		return vm.MakeNull()
	}
	b := br.buckets[br.pos]
	br.pos++
	return b
}

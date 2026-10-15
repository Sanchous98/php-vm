package php

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"phpvm/internal/vm"
	"strings"
	"syscall"
	"time"
)

// Пользовательские обёртки потока (stream_wrapper_register) и
// вспомогательные stream_* функции. Обёртка — PHP-класс с методами
// stream_open/stream_read/stream_write/stream_seek/stream_tell/stream_eof/
// stream_close/stream_stat/url_stat.

// streamWrappers — реестр пользовательских протоколов: схема -> имя класса.
var streamWrappers = map[string]string{}

// userStream — состояние открытого пользовательского потока.
type userStream struct {
	vm  *vm.VM
	ctx *vm.Context
	obj vm.Slot

	hasRead  bool
	hasWrite bool
	hasEof   bool
	hasTell  bool
	hasSeek  bool
	hasClose bool
}

// callMethod вызывает метод обёртки. Возвращает false, если вызов завершился
// исключением (оно уже выставлено на VM).
func (u *userStream) callMethod(name string, args ...vm.Slot) (vm.Slot, bool) {
	arr := vm.NewArray()
	arr.Append(u.obj)
	arr.Append(vm.MakeString(name))
	u.vm.MarkNextFrameInternal()
	res, err := u.vm.CallCallable(u.ctx, vm.MakeArray(arr), args)
	if err != nil {
		throwCallable(u.ctx, err)
		return vm.MakeNull(), false
	}
	return res, true
}

func (u *userStream) read(p []byte) (int, error) {
	if !u.hasRead {
		return 0, io.EOF
	}
	res, ok := u.callMethod("stream_read", vm.MakeInt(int64(len(p))))
	if !ok {
		return 0, errStreamReadFailed
	}
	if res.Type() == vm.BoolType && !res.ToBool() {
		return 0, errStreamReadFailed
	}
	s, cerr := u.vm.ToStringValue(res)
	if cerr != nil {
		throwCallable(u.ctx, cerr)
		return 0, errStreamReadFailed
	}
	n := copy(p, s)
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (u *userStream) write(p []byte) (int, error) {
	if !u.hasWrite {
		return 0, io.ErrShortWrite
	}
	res, ok := u.callMethod("stream_write", vm.MakeString(string(p)))
	if !ok {
		return 0, errStreamWriteFailed
	}
	if res.Type() == vm.BoolType && !res.ToBool() {
		return 0, errStreamWriteFailed
	}
	return int(res.ToInt()), nil
}

func (u *userStream) tell() (int64, error) {
	if u.hasTell {
		res, ok := u.callMethod("stream_tell")
		if ok {
			return res.ToInt(), nil
		}
	}
	return 0, nil
}

func (u *userStream) seek(off int64, whence int) (int64, error) {
	if u.hasSeek {
		res, ok := u.callMethod("stream_seek", vm.MakeInt(off), vm.MakeInt(int64(whence)))
		if ok && res.ToBool() {
			return u.tell()
		}
	}
	return 0, fmt.Errorf("stream does not support seeking")
}

func (u *userStream) eof() bool {
	v, _ := u.eofChecked()
	return v
}

// eofChecked возвращает значение stream_eof и признак успеха вызова
// (false — метод не реализован или бросил исключение; Zend в этом случае
// печатает Warning и считает поток завершённым).
func (u *userStream) eofChecked() (bool, bool) {
	if u.hasEof {
		res, ok := u.callMethod("stream_eof")
		if ok {
			return res.ToBool(), true
		}
	}
	return false, false
}

func (u *userStream) close() {
	if u.hasClose {
		u.callMethod("stream_close")
	}
}

// --- методы fsHandle, делегирующие пользовательской обёртке ---

// rawRead читает напрямую из источника (без буферизации).
func (h *fsHandle) rawRead(p []byte) (int, error) {
	switch {
	case h.us != nil:
		return h.us.read(p)
	case h.conn != nil:
		if h.readTimeout > 0 {
			h.conn.SetReadDeadline(time.Now().Add(h.readTimeout))
		} else {
			h.conn.SetReadDeadline(time.Time{})
		}
		n, err := h.conn.Read(p)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				h.timedOut = true
				return n, errStreamTimeout
			}
		}
		if n > 0 {
			h.timedOut = false
		}
		return n, err
	case h.ln != nil:
		return 0, fmt.Errorf("cannot read from listening socket")
	}
	return h.f.Read(p)
}

// bufferedRead читает из потока через буфер опережающего чтения. Используется
// для пользовательских обёрток и сокетов, чтобы не терять «лишние» байты,
// возвращённые stream_read/сетью, и корректно считать unread_bytes/feof.
func (h *fsHandle) bufferedRead(p []byte) (int, error) {
	// Пользовательская обёртка с размером чанка 1 читает без буферизации:
	// stream_read получает непосредственно запрошенный размер.
	if h.us != nil && h.chunk() == 1 {
		n, err := h.rawRead(p)
		if err == errStreamTimeout || err == errStreamReadFailed {
			return n, err
		}
		if n > 0 {
			h.rbof = false
			return n, err
		}
		h.rbof = true
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	if len(h.rbuf) == 0 {
		if h.rbof {
			return 0, io.EOF
		}
		buf := make([]byte, h.chunk())
		n, err := h.rawRead(buf)
		if n > 0 {
			h.rbuf = buf[:n]
		}
		if err != nil {
			if err != errStreamTimeout && err != errStreamReadFailed {
				h.rbof = true
			}
			if n == 0 {
				return 0, err
			}
		}
		if n == 0 && err == nil {
			h.rbof = true
			return 0, io.EOF
		}
	}
	n := copy(p, h.rbuf)
	h.rbuf = h.rbuf[n:]
	return n, nil
}

// errStreamTimeout — сентинел тайм-аута потоковой операции.
var errStreamTimeout = errors.New("stream timed out")

// errStreamReadFailed/errStreamWriteFailed — stream_read/stream_write
// пользовательской обёртки вернули false (сигнал ошибки ввода-вывода).
var (
	errStreamReadFailed  = errors.New("stream read failed")
	errStreamWriteFailed = errors.New("stream write failed")
)

// buffered поток (пользовательская обёртка, сокет или pipe с one-shot чтением)?
func (h *fsHandle) buffered() bool {
	return h.us != nil || h.conn != nil || h.singleRead
}

// Read читает из потока (обёртка, фильтры или файл).
func (h *fsHandle) Read(p []byte) (int, error) {
	if h.buffered() {
		return h.bufferedRead(p)
	}
	if h.hasReadFilters() {
		return h.filteredRead(p)
	}
	return h.f.Read(p)
}

// Write пишет в поток (обёртка, фильтры или файл).
func (h *fsHandle) Write(p []byte) (int, error) {
	if h.outVM != nil {
		n := len(p)
		if h.hasWriteFilters() {
			p = h.applyWriteFilters(p)
		}
		h.outVM.Write(string(p))
		return n, nil
	}
	if h.us != nil {
		// Пользовательская обёртка принимает запись порциями по размеру
		// внутреннего буфера (chunk), как в Zend.
		ch := h.chunk()
		if h.us.hasWrite && ch > 0 && len(p) > ch {
			total := 0
			for len(p) > 0 {
				n := ch
				if n > len(p) {
					n = len(p)
				}
				m, err := h.us.write(p[:n])
				total += m
				if err != nil {
					return total, err
				}
				p = p[n:]
			}
			return total, nil
		}
		return h.us.write(p)
	}
	if h.conn != nil {
		if h.shutWr {
			return 0, syscall.EPIPE
		}
		if h.writeTimeout > 0 {
			h.conn.SetWriteDeadline(time.Now().Add(h.writeTimeout))
		}
		return h.conn.Write(p)
	}
	if h.hasWriteFilters() {
		p = h.applyWriteFilters(p)
	}
	return h.f.Write(p)
}

// Seek позиционирует поток (обёртка или файл).
func (h *fsHandle) Seek(off int64, whence int) (int64, error) {
	if h.us != nil {
		return h.us.seek(off, whence)
	}
	if h.conn != nil || h.ln != nil {
		return 0, fmt.Errorf("stream does not support seeking")
	}
	return h.f.Seek(off, whence)
}

// Stat возвращает информацию о файле (для пользовательских обёрток не
// поддерживается без stream_stat).
func (h *fsHandle) Stat() (os.FileInfo, error) {
	if h.us != nil {
		return nil, fmt.Errorf("stat not supported")
	}
	if h.conn != nil {
		return nil, fmt.Errorf("stat not supported")
	}
	return h.f.Stat()
}

// Close закрывает поток (обёртка или файл).
func (h *fsHandle) Close() error {
	if h.us != nil {
		h.us.close()
		return nil
	}
	if h.conn != nil {
		return h.conn.Close()
	}
	if h.ln != nil {
		return h.ln.Close()
	}
	return h.f.Close()
}

// Sync сбрасывает буферы (для обёрток — no-op).
func (h *fsHandle) Sync() error {
	if h.us != nil {
		return nil
	}
	return h.f.Sync()
}

// Truncate усекает файл (для обёрток не поддерживается).
func (h *fsHandle) Truncate(n int64) error {
	if h.us != nil {
		if u := h.us; u != nil {
			return fmt.Errorf("truncate not supported")
		}
	}
	return h.f.Truncate(n)
}

// userScheme возвращает схему пути (до "://") и признак её наличия.
func userScheme(path string) (string, bool) {
	i := strings.Index(path, "://")
	if i <= 0 {
		return "", false
	}
	return path[:i], true
}

// openUserStream открывает пользовательскую обёртку: инстанцирует класс,
// вызывает stream_open и собирает список поддерживаемых методов.
// instantiateUserWrapper создаёт экземпляр класса пользовательской обёртки и
// выставляет его свойство context ресурсом текущего stream-контекста (Zend
// делает это при создании экземпляра; типизированное свойство несовместимого
// типа — TypeError, user_streams_context_001).
// ok=false — экземпляр создать не удалось (исключение в ctx или ошибка).
func instantiateUserWrapper(ctx *vm.Context, cls string) (vm.Slot, vm.ClassInfo, bool, error) {
	vmi := ctx.VMInstance()
	ci := vmi.FindClass(cls)
	if ci == nil {
		vmi.Autoload(ctx, cls)
		ci = vmi.FindClass(cls)
	}
	if ci == nil {
		return vm.Slot{}, nil, false, nil
	}
	slot, err := vmi.NewInstanceByNameFromInternal(cls, nil)
	if err != nil {
		return vm.Slot{}, ci, false, err
	}
	if derr := vmi.TakePendingDefaultError(); derr != nil {
		return vm.Slot{}, ci, false, derr
	}
	ensureDefaultStreamContext()
	if r := vm.AsResource(streamContextDefault); r != nil {
		if perr := vmi.SetPropertyChecked(slot, "context", streamContextDefault, false); perr != nil {
			return vm.Slot{}, ci, false, perr
		}
	}
	return slot, ci, true, nil
}

func openUserStream(ctx *vm.Context, path, mode string) (*fsHandle, bool) {
	scheme, ok := userScheme(path)
	if !ok {
		return nil, false
	}
	cls, ok := streamWrappers[strings.ToLower(scheme)]
	if !ok {
		return nil, false
	}
	obj, ci, ok, err := instantiateUserWrapper(ctx, cls)
	if !ok {
		if err != nil {
			if strings.Contains(err.Error(), "Cannot instantiate trait") || strings.Contains(err.Error(), "is abstract") {
				lastUserOpenErr = "operation failed"
				return nil, false
			}
			throwCallable(ctx, err)
			return nil, false
		}
		ctx.VM().Warning(fmt.Sprintf("stream_wrapper_register(): class '%s' not found", cls))
		return nil, false
	}
	u := &userStream{vm: ctx.VMInstance(), ctx: ctx, obj: obj}
	u.hasRead = hasMethod(ci, "stream_read")
	u.hasWrite = hasMethod(ci, "stream_write")
	u.hasEof = hasMethod(ci, "stream_eof")
	u.hasTell = hasMethod(ci, "stream_tell")
	u.hasSeek = hasMethod(ci, "stream_seek")
	u.hasClose = hasMethod(ci, "stream_close")

	canRead, canWrite := modeCaps(mode)
	h := &fsHandle{path: path, mode: mode, push: -1, canRead: canRead, canWrite: canWrite, us: u,
		appendMode: mode != "" && mode[0] == 'a'}
	h.meta = map[string]vm.Slot{"wrapper_type": vm.MakeString("user-space"), "stream_type": vm.MakeString("user-space"), "wrapper_data": obj}
	res, ok := u.callMethod("stream_open", vm.MakeString(path), vm.MakeString(mode), vm.MakeInt(0), vm.NewRef(vm.MakeNull()))
	if ctx.HasThrown() {
		return *new(*fsHandle), *new(bool)
	}
	if !ok || !res.ToBool() {
		lastUserOpenErr = fmt.Sprintf("%q call failed", cls+"::stream_open")
		return nil, false
	}
	return h, true
}

// lastUserOpenErr — причина неудачи открытия пользовательской обёртки
// (например, "operation failed" для неисполнимого класса).
var lastUserOpenErr string

// openUserDir открывает пользовательскую директорию (dir_opendir) и
// возвращает объект-обёртку для последующих dir_readdir/dir_closedir.
func openUserDir(ctx *vm.Context, path string) (*userStream, bool) {
	scheme, ok := userScheme(path)
	if !ok {
		return nil, false
	}
	cls, ok := streamWrappers[strings.ToLower(scheme)]
	if !ok {
		return nil, false
	}
	obj, ci, iok, err := instantiateUserWrapper(ctx, cls)
	if !iok {
		if err != nil {
			throwCallable(ctx, err)
			return nil, false
		}
		return nil, false
	}
	if !hasMethod(ci, "dir_opendir") {
		lastUserOpenErr = "operation failed"
		return nil, false
	}
	u := &userStream{vm: ctx.VMInstance(), ctx: ctx, obj: obj}
	res, ok := u.callMethod("dir_opendir", vm.MakeString(path), vm.MakeInt(0))
	if ctx.HasThrown() {
		return *new(*userStream), *new(bool)
	}
	if !ok || !res.ToBool() {
		return nil, false
	}
	return u, true
}

// hasMethod сообщает, объявлен ли в классе метод с именем name.
func hasMethod(ci vm.ClassInfo, name string) bool {
	_, ok := ci.MethodFunction(name)
	return ok
}

// callUserWrapper инстанцирует пользовательскую обёртку для URL и вызывает её
// метод (url_stat/unlink/rename/mkdir/rmdir/dir_opendir/stream_metadata).
// Возвращает (результат, обработано).
func callUserWrapper(ctx *vm.Context, url, method string, args ...vm.Slot) (vm.Slot, bool) {
	scheme, ok := userScheme(url)
	if !ok {
		return vm.MakeNull(), false
	}
	cls, ok := streamWrappers[strings.ToLower(scheme)]
	if !ok {
		return vm.MakeNull(), false
	}
	vmi := ctx.VMInstance()
	ci := vmi.FindClass(cls)
	if ci == nil {
		vmi.Autoload(ctx, cls)
		ci = vmi.FindClass(cls)
	}
	if ci == nil {
		return vm.MakeNull(), false
	}
	if !hasMethod(ci, method) {
		return vm.MakeNull(), false
	}
	obj, err := vmi.NewInstanceByNameFromInternal(cls, nil)
	if err != nil {
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return *new(vm.Slot), *new(bool)
		}
		return vm.MakeNull(), true
	}
	u := &userStream{vm: vmi, ctx: ctx, obj: obj}
	res, ok := u.callMethod(method, args...)
	if ctx.HasThrown() {
		return *new(vm.Slot), *new(bool)
	}
	if !ok {
		return vm.MakeNull(), true
	}
	return res, true
}

// deepCopySlot рекурсивно копирует массив, разыменовывая элементы-ссылки
// (снимок на момент вызова, как при копировании массива в Zend).
func deepCopySlot(s vm.Slot) vm.Slot {
	if s.IsRef() && s.Ref() != nil {
		s = s.Ref().Value()
	}
	if s.Type() != vm.ArrayType {
		return s
	}
	a := s.AsArray()
	na := vm.NewArray()
	for k, v := range a.All() {
		na.Set(k, deepCopySlot(v))
	}
	return vm.MakeArray(na)
}

// --- stream_wrapper_* ---

func streamWrapperRegisterFn(ctx *vm.Context) vm.Value {
	name := arg0(ctx).ToString()
	class := arg(ctx, 1).ToString()
	if class == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "stream_wrapper_register(): Argument #2 ($class) must not be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	ci := ctx.VMInstance().FindClass(class)
	if ci == nil {
		ctx.VMInstance().Autoload(ctx, class)
		ci = ctx.VMInstance().FindClass(class)
	}
	if ci == nil {
		ctx.Throw(vm.NewThrowable("TypeError",
			fmt.Sprintf("stream_wrapper_register(): Argument #2 ($class) must be a valid class name, %s given", class)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	streamWrappers[strings.ToLower(name)] = class
	if len(ctx.Args()) >= 3 && arg(ctx, 2).ToInt()&1 != 0 {
		streamWrapperURL[strings.ToLower(name)] = true
	} else {
		delete(streamWrapperURL, strings.ToLower(name))
	}
	return vm.Bool(true)
}

// streamWrapperURL — пользовательские обёртки, зарегистрированные с
// STREAM_IS_URL (подчиняются allow_url_include/allow_url_fopen).
var streamWrapperURL = map[string]bool{}

// openForInclude — идёт открытие потока для include/require: вложенные
// пользовательские URL-обёртки тогда дополнительно проверяют allow_url_include.
var openForInclude bool

// urlWrapperBlocked сообщает причину блокировки URL-обёртки scheme
// ("allow_url_fopen" / "allow_url_include") либо "", false.
func urlWrapperBlocked(scheme string) (string, bool) {
	if !streamWrapperURL[strings.ToLower(scheme)] {
		return "", false
	}
	if openForInclude {
		if !iniEnabled("allow_url_include") {
			return "allow_url_include", true
		}
		if !iniEnabled("allow_url_fopen") {
			return "allow_url_fopen", true
		}
		return "", false
	}
	if !iniEnabled("allow_url_fopen") {
		return "allow_url_fopen", true
	}
	return "", false
}

// warnWrapperDisabled печатает пару предупреждений о выключенной обёртке:
// как fopen()/file_get_contents()/include() видят её в php-src.
func warnWrapperDisabled(ctx *vm.Context, fn, scheme, path, reason string) {
	ctx.VM().Warning(fn + "(): " + scheme + ":// wrapper is disabled in the server configuration by " + reason + "=0")
	ctx.VM().Warning(fn + "(" + path + "): Failed to open stream: no suitable wrapper could be found")
}

func streamWrapperUnregisterFn(ctx *vm.Context) vm.Value {
	name := strings.ToLower(arg0(ctx).ToString())
	if _, ok := streamWrappers[name]; ok {
		delete(streamWrappers, name)
		return vm.Bool(true)
	}
	if isBuiltinScheme(name) {
		// Отключаем встроенную обёртку (аналог unregister в php-src).
		disabledWrappers[name] = true
		return vm.Bool(true)
	}
	ctx.VM().Warning(fmt.Sprintf("stream_wrapper_unregister(): Unable to unregister the user-space stream wrapper \"%s\"", arg0(ctx).ToString()))
	return vm.Bool(false)
}

// isBuiltinScheme сообщает, является ли схема встроенной потоковой обёрткой.
func isBuiltinScheme(name string) bool {
	switch name {
	case "file", "http", "https", "ftp", "ftps", "php", "compress.zlib", "compress.bzip2",
		"compress.zstd", "data", "glob", "phar", "ssh2", "rar", "ogg", "expect":
		return true
	}
	return false
}

// disabledWrappers — встроенные обёртки, отключённые stream_wrapper_unregister.
var disabledWrappers = map[string]bool{}

func streamWrapperRestoreFn(ctx *vm.Context) vm.Value {
	name := strings.ToLower(arg0(ctx).ToString())
	if disabledWrappers[name] {
		delete(disabledWrappers, name)
		return vm.Bool(true)
	}
	// Встроенные обёртки не поддерживаются пользовательской реализацией;
	// восстановление встроенной обёртки — no-op.
	if _, ok := streamWrappers[name]; ok {
		delete(streamWrappers, name)
		return vm.Bool(true)
	}
	switch name {
	case "file", "http", "ftp", "php", "compress.zlib", "data", "glob", "phar":
		return vm.Bool(true)
	}
	ctx.VM().Warning(fmt.Sprintf("stream_wrapper_restore(): Unable to restore the user-space stream wrapper \"%s\"", arg0(ctx).ToString()))
	return vm.Bool(false)
}

func streamGetWrappersFn(ctx *vm.Context) vm.Value {
	builtin := []string{"https", "ftps", "compress.zlib", "php", "file", "glob", "data", "http", "ftp", "phar"}
	out := vm.NewArray()
	for _, w := range builtin {
		out.Append(vm.MakeString(w))
	}
	for scheme := range streamWrappers {
		out.Append(vm.MakeString(scheme))
	}
	return vm.MakeArray(out)
}

func streamIsLocalFn(ctx *vm.Context) vm.Value {
	path := arg0(ctx).ToString()
	if strings.HasPrefix(path, "file://") || !strings.Contains(path, "://") {
		return vm.Bool(true)
	}
	if scheme, ok := userScheme(path); ok {
		if _, ok := streamWrappers[strings.ToLower(scheme)]; ok {
			return vm.Bool(true)
		}
	}
	return vm.Bool(false)
}

func streamResolveIncludePathFn(ctx *vm.Context) vm.Value {
	path, ok := fsResolveIncludePath(arg0(ctx).ToString(), ctx)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !ok {
		return vm.Bool(false)
	}
	return vm.MakeString(path)
}

func streamSupportsLockFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_supports_lock")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.us != nil || h.isMemory {
		return vm.Bool(false)
	}
	if strings.HasPrefix(h.path, "data:") || strings.HasPrefix(h.path, "php://") {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// --- stream_context_create ---

// streamContext — непрозрачный ресурс контекста потока. Настройки пока
// хранятся, но не влияют на ввод-вывод.
type streamContext struct {
	options vm.Slot
	params  vm.Slot
}

func streamContextCreateFn(ctx *vm.Context) vm.Value {
	opts := vm.MakeArray(vm.NewArray())
	if len(ctx.Args()) >= 1 && arg0(ctx).Type() == vm.ArrayType {
		opts = deepCopySlot(arg0(ctx))
	}
	params := vm.MakeArray(vm.NewArray())
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() == vm.ArrayType {
		params = arg(ctx, 1)
	}
	c := &streamContext{options: opts, params: params}
	id := fsNextID
	fsNextID++
	return vm.MakeResource(id, "stream-context", c)
}

func streamContextGetOptionsFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg0(ctx))
	if r == nil || r.Kind != "stream-context" {
		return vm.Bool(false)
	}
	if c, ok := r.Val.(*streamContext); ok {
		return c.options
	}
	return vm.MakeArray(vm.NewArray())
}

func streamContextSetOptionFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg0(ctx))
	if r == nil || r.Kind != "stream-context" {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("stream_context_set_option", 1, "context", "resource", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	c, ok := r.Val.(*streamContext)
	if !ok {
		return vm.Bool(false)
	}
	n := len(ctx.Args())
	arg1 := arg(ctx, 1)
	if arg1.Type() == vm.ArrayType {
		if n >= 3 && arg(ctx, 2).Type() != vm.NullType {
			ctx.Throw(vm.NewThrowable("Error", "stream_context_set_option(): Argument #3 ($option_name) must be null when argument #2 ($wrapper_or_options) is an array"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
		if n >= 4 {
			ctx.Throw(vm.NewThrowable("Error", "stream_context_set_option(): Argument #4 ($value) cannot be provided when argument #2 ($wrapper_or_options) is an array"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
		mergeOptions(c, arg1.AsArray())
		return vm.Bool(true)
	}
	if n < 3 || arg(ctx, 2).Type() == vm.NullType {
		ctx.Throw(vm.NewThrowable("Error", "stream_context_set_option(): Argument #3 ($option_name) cannot be null when argument #2 ($wrapper_or_options) is a string"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if n < 4 {
		ctx.Throw(vm.NewThrowable("Error", "stream_context_set_option(): Argument #4 ($value) must be provided when argument #2 ($wrapper_or_options) is a string"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	wrap := arg1.ToString()
	name := arg(ctx, 2).ToString()
	val := arg(ctx, 3)
	setContextOption(c, wrap, name, val)
	return vm.Bool(true)
}

// mergeOptions сливает массив опций {$wrapper: {$name: $value}} в контекст.
func mergeOptions(c *streamContext, opts *vm.Array) {
	if opts == nil {
		return
	}
	for k, v := range opts.All() {
		sub := v.AsArray()
		if sub == nil {
			continue
		}
		for name, val := range sub.All() {
			setContextOption(c, k.StrVal(), name.StrVal(), val)
		}
	}
}

// setContextOption устанавливает одну опцию обёртки.
func setContextOption(c *streamContext, wrap, name string, val vm.Slot) {
	if c.options.Type() != vm.ArrayType {
		c.options = vm.MakeArray(vm.NewArray())
	}
	base := c.options.AsArray().Unshare()
	m, _ := base.Get(vm.MakeStrKey(wrap))
	sub := m.AsArray()
	if sub == nil {
		sub = vm.NewArray()
	} else {
		sub = sub.Unshare()
	}
	sub.SetStr(name, val)
	base.SetStr(wrap, vm.MakeArray(sub))
	c.options = vm.MakeArray(base)
}

// streamContextDefault — контекст по умолчанию (stream_context_get_default).
var streamContextDefault vm.Slot

// fsDefaultCtx — был ли уже выделен ресурс контекста по умолчанию. В PHP он
// создаётся лениво при первой обёрточной операции (php_stream_context_from_zval
// с NULL: fopen/file_get_contents/opendir/socket-функции...), занимая id 4.
var fsDefaultCtx bool

// ensureDefaultStreamContext лениво создаёт контекст по умолчанию — так же, как
// php_stream_context_get_default() в PHP. Вызывается на входе в обёрточные
// операции; proc_open-пайпы (сырые fd) контекст не трогают.
func ensureDefaultStreamContext() {
	if fsDefaultCtx {
		return
	}
	fsDefaultCtx = true
	id := fsNextID
	fsNextID++
	streamContextDefault = vm.MakeResource(id, "stream-context", &streamContext{
		options: vm.MakeArray(vm.NewArray()),
		params:  vm.MakeArray(vm.NewArray()),
	})
}

func streamContextGetDefaultFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) >= 1 && arg0(ctx).Type() == vm.ArrayType {
		if !validContextOptions(arg0(ctx).AsArray()) {
			ctx.Throw(vm.NewThrowable("ValueError", `Options should have the form ["wrappername"]["optionname"] = $value`))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
	}
	if streamContextDefault.Type() == vm.ResourceType {
		r := vm.AsResource(streamContextDefault)
		if r != nil && !r.Closed {
			return streamContextDefault
		}
	}
	ensureDefaultStreamContext()
	return streamContextDefault
}

// streamContextSetOptionsFn — stream_context_set_options($context, $options).
func streamContextSetOptionsFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg0(ctx))
	if r == nil || r.Kind != "stream-context" {
		ctx.Throw(vm.NewThrowable("TypeError", "stream_context_set_options(): Argument #1 ($context) must be a valid stream/context"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	c, ok := r.Val.(*streamContext)
	if !ok || arg(ctx, 1).Type() != vm.ArrayType {
		return vm.Bool(false)
	}
	c.options = arg(ctx, 1)
	return vm.Bool(true)
}

func streamContextGetParamsFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg0(ctx))
	if r == nil || r.Kind != "stream-context" {
		return vm.Bool(false)
	}
	c, ok := r.Val.(*streamContext)
	if !ok {
		return vm.Bool(false)
	}
	out := c.params.AsArray()
	if out == nil {
		out = vm.NewArray()
	}
	if _, exists := out.Get(vm.MakeStrKey("options")); !exists {
		out.SetStr("options", c.options)
	}
	return vm.MakeArray(out)
}

func streamContextSetParamsFn(ctx *vm.Context) vm.Value {
	r := vm.AsResource(arg0(ctx))
	if r == nil || r.Kind != "stream-context" {
		return vm.Bool(false)
	}
	c, ok := r.Val.(*streamContext)
	if !ok || arg(ctx, 1).Type() != vm.ArrayType {
		return vm.Bool(false)
	}
	if v, ok := arg(ctx, 1).AsArray().Get(vm.MakeStrKey("options")); ok && v.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", "Invalid stream/context parameter"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	c.params = arg(ctx, 1)
	return vm.Bool(true)
}

// validContextOptions проверяет форму массива опций контекста:
// каждый элемент — массив с именем обёртки-строкой.
func validContextOptions(a *vm.Array) bool {
	if a == nil {
		return true
	}
	for k, v := range a.All() {
		if !k.IsStr() || v.Type() != vm.ArrayType {
			return false
		}
	}
	return true
}

func streamGetTransportsFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, t := range []string{"tcp", "udp", "unix", "udg", "ssl", "tls", "tlsv1.0", "tlsv1.1", "tlsv1.2", "tlsv1.3"} {
		out.Append(vm.MakeString(t))
	}
	return vm.MakeArray(out)
}

func setFileBufferFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_set_write_buffer")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	n := arg(ctx, 1).ToInt()
	// STREAM_OPTION_WRITE_BUFFER: значение = STREAM_BUFFER_NONE(0) при n==0,
	// иначе STREAM_BUFFER_FULL(2); ptrparam = размер буфера.
	val := int64(2)
	if n == 0 {
		val = 0
	}
	if r, ok := userSetOption(ctx, h, "stream_set_write_buffer", 3, val, vm.MakeInt(n)); ok {
		if r {
			return vm.Int(0)
		}
		return vm.Int(-1)
	}
	return vm.Int(0)
}

// setReadFileBufferFn — stream_set_read_buffer(): как write-вариант, но
// STREAM_OPTION_READ_BUFFER (для обычных потоков — no-op, успех).
func setReadFileBufferFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_set_read_buffer")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	n := arg(ctx, 1).ToInt()
	val := int64(2)
	if n == 0 {
		val = 0
	}
	if r, ok := userSetOption(ctx, h, "stream_set_read_buffer", 2, val, vm.MakeInt(n)); ok {
		if r {
			return vm.Int(0)
		}
		return vm.Int(-1)
	}
	return vm.Int(0)
}

package php

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"phpvm/internal/vm"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Сетевые потоковые функции (stream_socket_*, fsockopen) и stream_select.
// Реализованы поверх net.Conn/net.Listener (TCP/unix), а для
// stream_socket_pair — поверх syscall.Socketpair.

// setRefArg записывает значение v в by-ref аргумент #i (если он by-ref).
func setRefArg(ctx *vm.Context, i int, v vm.Slot) {
	s := arg(ctx, i)
	if s.IsRef() && s.Ref() != nil {
		s.Ref().Set(v)
	}
}

// parseSocketURI разбирает URI сокета на схему и адрес для net.Dial/Listen.
func parseSocketURI(uri string) (scheme, addr string) {
	if i := strings.Index(uri, "://"); i >= 0 {
		scheme = strings.ToLower(uri[:i])
		addr = uri[i+3:]
	} else {
		scheme = "tcp"
		addr = uri
	}
	addr = stripPortSuffix(addr)
	addr = maskPort(addr)
	return scheme, addr
}

// stripPortSuffix отбрасывает «хвост» после цифр порта: PHP парсит порт через
// strtol и игнорирует остаток адреса ('host:10000/client0' → host:10000).
func stripPortSuffix(addr string) string {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return addr
	}
	port := addr[i+1:]
	j := 0
	for j < len(port) && port[j] >= '0' && port[j] <= '9' {
		j++
	}
	if j == 0 || j == len(port) {
		return addr
	}
	return addr[:i+1] + port[:j]
}

// maskPort приводит порт к 16-битному диапазону (PHP/getaddrinfo допускают
// порты > 65535, усекая их по модулю 65536).
func maskPort(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 65535 {
		return addr
	}
	return net.JoinHostPort(host, strconv.Itoa(p&0xffff))
}

// socketFromConn создаёт fsHandle для установленного соединения.
func socketFromConn(c net.Conn, uri string, mode string) *fsHandle {
	st := "tcp_socket"
	scheme, _ := parseSocketURI(uri)
	switch {
	case scheme == "udp" || scheme == "udg":
		st = "udp_socket"
	case strings.HasPrefix(scheme, "udp"):
		st = "udp_socket"
	}
	if _, ok := c.(*net.UnixConn); ok {
		st = "unix_socket"
	}
	h := &fsHandle{
		path: uri, mode: mode, push: -1,
		canRead: true, canWrite: true, isSocket: true,
		sockType: st, blockMode: true, conn: c,
	}
	return h
}

// streamSocketServerFn — stream_socket_server().
func streamSocketServerFn(ctx *vm.Context) vm.Value {
	ensureDefaultStreamContext()
	uri := arg0(ctx).ToString()
	scheme, addr := parseSocketURI(uri)
	var (
		ln   net.Listener
		err  error
		sock string
		uc   net.Conn
	)
	switch scheme {
	case "tcp", "tcp4", "tcp6":
		ln, err = net.Listen("tcp", addr)
		sock = "tcp_socket"
	case "udp", "udp4", "udp6":
		a, e := net.ResolveUDPAddr("udp", addr)
		if e != nil {
			err = e
			break
		}
		var pc net.PacketConn
		pc, err = net.ListenUDP("udp", a)
		if err == nil {
			// UDP-сервер: чтение/запись датаграмм через тот же conn.
			uc = pc.(net.Conn)
		}
		sock = "udp_socket"
	case "unix":
		_ = os.Remove(addr)
		ln, err = net.Listen("unix", addr)
		sock = "unix_socket"
	default:
		err = &transportError{scheme: scheme}
	}
	if err != nil {
		setRefArg(ctx, 1, vm.MakeInt(0))
		setRefArg(ctx, 2, vm.MakeString(err.Error()))
		ctx.VM().Warning(fmt.Sprintf("stream_socket_server(): Unable to connect to %s (%s)", uri, err.Error()))
		return vm.Bool(false)
	}
	setRefArg(ctx, 1, vm.MakeInt(0))
	setRefArg(ctx, 2, vm.MakeString(""))
	h := &fsHandle{
		path: uri, mode: "r+", push: -1,
		canRead: true, canWrite: true, isSocket: true,
		sockType: sock, blockMode: true, ln: ln, conn: uc,
	}
	return fsNewResource(h, "stream")
}

// socketParseError — ошибка разбора адреса (в PHP errcode=0, а не 111).
type socketParseError struct{ msg string }

func (e *socketParseError) Error() string { return e.msg }

// transportError — неизвестная транспортная схема (errno 0).
type transportError struct{ scheme string }

func (e *transportError) Error() string {
	return `Unable to find the socket transport "` + e.scheme + `" - did you forget to enable it when you configured PHP?`
}

// dnsFailure переводит ошибку DNS в текст php_network_getaddresses.
func dnsFailure(err error) (string, bool) {
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		return "", false
	}
	reason := "Name or service not known"
	if dnsErr.IsTimeout {
		reason = "Temporary failure in name resolution"
	}
	return "php_network_getaddresses: getaddrinfo for " + dnsErr.Name + " failed: " + reason, true
}

// socketErrInfo возвращает PHP errno и текст причины для ошибки соединения.
func socketErrInfo(err error) (int64, string) {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		msg, _ := dnsFailure(err)
		return 0, msg
	}
	var pe *socketParseError
	if errors.As(err, &pe) {
		return 0, pe.msg
	}
	var te *transportError
	if errors.As(err, &te) {
		return 0, te.msg()
	}
	if strings.Contains(err.Error(), "null bytes") {
		return 0, "The hostname must not contain null bytes"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ECONNREFUSED:
			return 111, "Connection refused"
		case syscall.ETIMEDOUT:
			return 110, "Connection timed out"
		case syscall.ENETUNREACH:
			return 101, "Network is unreachable"
		case syscall.EHOSTUNREACH:
			return 113, "No route to host"
		case syscall.EADDRNOTAVAIL:
			return 99, "Cannot assign requested address"
		case syscall.EACCES:
			return 13, "Permission denied"
		}
		s := errno.Error()
		if s != "" {
			s = strings.ToUpper(s[:1]) + s[1:]
		}
		return int64(errno), s
	}
	return 111, err.Error()
}

func (e *transportError) msg() string { return e.Error() }

// dialDisplayHost повторяет имя в диагностике fsockopen: hostname:port, причём
// NUL обрезает имя (PHP форматирует буфер как C-строку).
func dialDisplayHost(host string, port int64) string {
	if i := strings.IndexByte(host, 0); i >= 0 {
		host = host[:i]
	}
	return host + ":" + strconv.FormatInt(port, 10)
}

// socketAddrParseErr повторяет проверку адреса host:port из php_network:
// stream_socket_client/fsockclose требуют явный порт; иначе — «Failed to parse».
func socketAddrParseErr(addr string) string {
	if addr == "" {
		return `Failed to parse address ""`
	}
	if addr[0] == '[' {
		end := strings.IndexByte(addr, ']')
		if end < 0 {
			if len(addr) == 1 {
				return `Failed to parse address "["`
			}
			return `Failed to parse IPv6 address "` + addr + `"`
		}
		host := addr[1:end]
		rest := addr[end+1:]
		validHost := host == "" || net.ParseIP(host) != nil
		if !validHost || rest == "" || !strings.HasPrefix(rest, ":") {
			return `Failed to parse IPv6 address "` + addr + `"`
		}
		if _, err := strconv.Atoi(rest[1:]); err != nil {
			return `Failed to parse IPv6 address "` + addr + `"`
		}
		return ""
	}
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return `Failed to parse address "` + addr + `"`
	}
	host, port := addr[:i], addr[i+1:]
	if port == "" || strings.ContainsAny(host, "[]") {
		return `Failed to parse address "` + addr + `"`
	}
	if _, err := strconv.Atoi(port); err != nil {
		return `Failed to parse address "` + addr + `"`
	}
	return ""
}

// dialSocket выполняет установку соединения согласно URI.
func dialSocket(uri string, timeout float64, persistent bool) (net.Conn, error) {
	scheme, addr := parseSocketURI(uri)
	if strings.IndexByte(addr, 0) >= 0 || strings.IndexByte(uri, 0) >= 0 {
		return nil, fmt.Errorf("The hostname must not contain null bytes")
	}
	switch scheme {
	case "tcp", "tcp4", "tcp6", "ssl", "tls", "tlsv1.0", "tlsv1.1", "tlsv1.2", "tlsv1.3", "http", "https", "ftp", "ftps", "gopher", "ssh2", "udp", "udg":
		if pe := socketAddrParseErr(addr); pe != "" {
			return nil, &socketParseError{pe}
		}
	}
	d := net.Dialer{Timeout: time.Duration(timeout * float64(time.Second))}
	switch scheme {
	case "tcp", "tcp4", "tcp6", "ssl", "tls", "tlsv1.0", "tlsv1.1", "tlsv1.2", "tlsv1.3", "http", "https", "ftp", "ftps", "gopher", "ssh2":
		return d.Dial("tcp", addr)
	case "udp", "udg":
		return d.Dial("udp", addr)
	case "unix":
		return d.Dial("unix", addr)
	default:
		return nil, &transportError{scheme: scheme}
	}
}

// streamSocketClientFn — stream_socket_client().
func streamSocketClientFn(ctx *vm.Context) vm.Value {
	ensureDefaultStreamContext()
	uri := arg0(ctx).ToString()
	timeout := 60.0
	if len(ctx.Args()) >= 4 && arg(ctx, 3).Type() != vm.NullType {
		f := arg(ctx, 3).ToFloat()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			ctx.Throw(vm.NewThrowable("ValueError", "stream_socket_client(): Argument #4 ($timeout) must be a finite value"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
		timeout = f
	}
	flags := int64(0)
	if len(ctx.Args()) >= 5 {
		flags = arg(ctx, 4).ToInt()
	}
	persistent := flags&2 != 0
	c, err := dialSocket(uri, timeout, persistent)
	if err != nil {
		if persistent && timeout == 0 {
			// PHP при persistent-соединении с нулевым тайм-аутом и
			// неразбираемым адресом возвращает «persistent stream» без Warning.
			f, _ := os.CreateTemp("", "php-persist")
			h := &fsHandle{f: f, path: uri, mode: "r+", push: -1, canRead: true, canWrite: true, tmp: true}
			setRefArg(ctx, 1, vm.MakeInt(0))
			setRefArg(ctx, 2, vm.MakeString(""))
			return fsNewResource(h, "persistent stream")
		}
		errno := int64(111)
		if _, ok := err.(*socketParseError); ok {
			errno = 0
		}
		msg := err.Error()
		if dnsMsg, ok := dnsFailure(err); ok {
			// PHP печатает две диагностики: от php_network_getaddresses и
			// от самого stream_socket_client (bindto).
			ctx.VM().Warning("stream_socket_client(): " + dnsMsg)
			msg = dnsMsg
			errno = 0
		} else {
			errno, msg = socketErrInfo(err)
		}
		setRefArg(ctx, 1, vm.MakeInt(errno))
		setRefArg(ctx, 2, vm.MakeString(msg))
		disp := strings.ReplaceAll(uri, "\x00", `\0`)
		ctx.VM().Warning(fmt.Sprintf("stream_socket_client(): Unable to connect to %s (%s)", disp, msg))
		return vm.Bool(false)
	}
	kind := "stream"
	if persistent {
		kind = "persistent stream"
	}
	return fsNewResource(socketFromConn(c, uri, "r+"), kind)
}

// persistentStreams — кэш постоянных соединений pfsockopen (uri -> ресурс).
var persistentStreams = map[string]vm.Slot{}

// fsockopenFn — fsockopen()/pfsockopen().
//
//	fsockopen(string $hostname, int $port = -1, &$error_code, &$error_message, ?float $timeout)
func fsockopenFn(ctx *vm.Context) vm.Value  { return fsockopenImpl(ctx, false) }
func pfsockopenFn(ctx *vm.Context) vm.Value { return fsockopenImpl(ctx, true) }

func fsockopenImpl(ctx *vm.Context, persistent bool) vm.Value {
	host := arg0(ctx).ToString()
	port := int64(-1)
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() != vm.NullType {
		port = arg(ctx, 1).ToInt()
	}
	if !strings.Contains(host, "://") {
		host = "tcp://" + host
	}
	// Добавляем порт, если он задан и ещё не указан в адресе.
	if port > 0 {
		authority := host[strings.Index(host, "://")+3:]
		if !strings.Contains(authority, ":") {
			host = host + ":" + strconv.FormatInt(port, 10)
		}
	}
	if persistent {
		if s, ok := persistentStreams[host]; ok {
			if r := vm.AsResource(s); r != nil && !r.Closed {
				return s
			}
		}
	}
	timeout := 60.0
	if len(ctx.Args()) >= 5 && arg(ctx, 4).Type() != vm.NullType {
		timeout = arg(ctx, 4).ToFloat()
	}
	c, err := dialSocket(host, timeout, persistent)
	if err != nil {
		errno, msg := socketErrInfo(err)
		setRefArg(ctx, 2, vm.MakeInt(errno))
		setRefArg(ctx, 3, vm.MakeString(msg))
		// PHP предупреждает и для fsockopen; имя — hostname:port из аргументов.
		ctx.VM().Warning(fmt.Sprintf("fsockopen(): Unable to connect to %s (%s)", dialDisplayHost(arg0(ctx).ToString(), port), msg))
		return vm.Bool(false)
	}
	kind := "stream"
	if persistent {
		kind = "persistent stream"
	}
	s := fsNewResource(socketFromConn(c, host, "r+"), kind)
	if persistent {
		persistentStreams[host] = s
	}
	return s
}

// streamSocketPairFn — stream_socket_pair().
func streamSocketPairFn(ctx *vm.Context) vm.Value {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		ctx.VM().Warning("stream_socket_pair(): Unable to create socket pair")
		return vm.Bool(false)
	}
	arr := vm.NewArray()
	for _, fd := range fds {
		f := os.NewFile(uintptr(fd), "socketpair")
		h := &fsHandle{f: f, path: "socketpair", mode: "r+", push: -1,
			canRead: true, canWrite: true, isSocket: true, sockType: "unix_socket", blockMode: true}
		arr.Append(fsNewResource(h, "stream"))
	}
	return vm.MakeArray(arr)
}

// streamSocketAcceptFn — stream_socket_accept().
func streamSocketAcceptFn(ctx *vm.Context) vm.Value {
	ensureDefaultStreamContext()
	h := streamArg(ctx, 0, "stream_socket_accept")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.ln == nil {
		ctx.VM().Warning("stream_socket_accept(): The given stream does not represent a socket server")
		return vm.Bool(false)
	}
	timeout := -1.0
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() != vm.NullType {
		f := arg(ctx, 1).ToFloat()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			ctx.Throw(vm.NewThrowable("ValueError", "stream_socket_accept(): Argument #2 ($timeout) must be a finite value"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
		timeout = f
	}
	if timeout >= 0 {
		if tl, ok := h.ln.(*net.TCPListener); ok {
			tl.SetDeadline(time.Now().Add(time.Duration(timeout * float64(time.Second))))
			defer tl.SetDeadline(time.Time{})
		}
	}
	c, err := h.ln.Accept()
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return vm.Bool(false)
		}
		ctx.VM().Warning("stream_socket_accept(): Accept failed: " + err.Error())
		return vm.Bool(false)
	}
	if len(ctx.Args()) >= 3 {
		setRefArg(ctx, 2, vm.MakeString(c.RemoteAddr().String()))
	}
	nh := socketFromConn(c, h.path, "r+")
	nh.sockType = h.sockType
	return fsNewResource(nh, "stream")
}

// streamSocketGetNameFn — stream_socket_get_name().
func streamSocketGetNameFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_socket_get_name")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	wantPeer := arg(ctx, 1).ToBool()
	if h.conn != nil {
		if wantPeer {
			if h.conn.RemoteAddr() == nil {
				return vm.Bool(false)
			}
			return vm.MakeString(h.conn.RemoteAddr().String())
		}
		if h.conn.LocalAddr() == nil {
			return vm.Bool(false)
		}
		return vm.MakeString(h.conn.LocalAddr().String())
	}
	if h.ln != nil {
		if wantPeer {
			return vm.Bool(false)
		}
		return vm.MakeString(h.ln.Addr().String())
	}
	return vm.Bool(false)
}

// streamSocketShutdownFn — stream_socket_shutdown().
func streamSocketShutdownFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_socket_shutdown")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	how := int(arg(ctx, 1).ToInt())
	if h.conn != nil {
		switch how {
		case 0:
			if c, ok := h.conn.(interface{ CloseRead() error }); ok {
				c.CloseRead()
			}
			h.shutRd = true
		case 1:
			if c, ok := h.conn.(interface{ CloseWrite() error }); ok {
				c.CloseWrite()
			}
			h.shutWr = true
		default:
			h.shutRd, h.shutWr = true, true
			h.conn.Close()
		}
		return vm.Bool(true)
	}
	if h.f != nil {
		_ = syscall.Shutdown(int(h.f.Fd()), how)
		return vm.Bool(true)
	}
	return vm.Bool(false)
}

// streamSocketRecvfromFn — stream_socket_recvfrom().
func streamSocketRecvfromFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_socket_recvfrom")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.conn == nil {
		if len(ctx.Args()) >= 4 {
			setRefArg(ctx, 3, vm.MakeNull())
		}
		return vm.Bool(false)
	}
	// Чтение только из буфера/соединения; для неблокирующего режима — 0.
	n := int(arg(ctx, 1).ToInt())
	if n <= 0 {
		return vm.Bool(false)
	}
	buf := make([]byte, n)
	m, err := h.Read(buf)
	if err != nil || m == 0 {
		return vm.Bool(false)
	}
	if len(ctx.Args()) >= 4 {
		setRefArg(ctx, 3, vm.MakeString(h.conn.RemoteAddr().String()))
	}
	return vm.MakeString(string(buf[:m]))
}

// streamSocketSendtoFn — stream_socket_sendto().
func streamSocketSendtoFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_socket_sendto")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if h.conn == nil {
		return vm.Bool(false)
	}
	data := argToStringVal(ctx, 1)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	n, err := h.Write([]byte(data))
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Int(int64(n))
}

// streamSetBlockingFn — stream_set_blocking().
// userSetOption вызывает stream_set_option пользовательской обёртки.
// Возвращает (значение метода как bool, обработано).
func userSetOption(ctx *vm.Context, h *fsHandle, fn string, option, value int64, ptr vm.Slot) (bool, bool) {
	if h.us == nil {
		return false, false
	}
	o := h.us.vm.SlotObject(h.us.obj)
	var ci vm.ClassInfo
	if o != nil {
		ci = h.us.vm.FindClass(o.ClassName())
	}
	name := ""
	if o != nil {
		name = o.ClassName()
	}
	if ci == nil || !hasMethod(ci, "stream_set_option") {
		ctx.VM().Warning(fmt.Sprintf("%s(): %s::stream_set_option is not implemented!", fn, name))
		return false, true
	}
	res, ok := h.us.callMethod("stream_set_option", vm.MakeInt(option), vm.MakeInt(value), ptr)
	if !ok {
		return false, true
	}
	return res.ToBool(), true
}

func streamSetBlockingFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_set_blocking")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	mode := arg(ctx, 1).ToBool()
	v := int64(0)
	if mode {
		v = 1
	}
	if r, ok := userSetOption(ctx, h, "stream_set_blocking", 1, v, vm.MakeNull()); ok {
		return vm.Bool(r)
	}
	h.blockMode = mode
	return vm.Bool(true)
}

// streamSetTimeoutFn — stream_set_timeout()/socket_set_timeout().
func streamSetTimeoutFn(ctx *vm.Context) vm.Value {
	h := streamArg(ctx, 0, "stream_set_timeout")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	sec := int(arg(ctx, 1).ToInt())
	usec := 0
	if len(ctx.Args()) >= 3 {
		usec = int(arg(ctx, 2).ToInt())
	}
	if r, ok := userSetOption(ctx, h, "stream_set_timeout", 4, int64(sec), vm.MakeInt(int64(usec))); ok {
		return vm.Bool(r)
	}
	if h.conn == nil {
		// Потоки, не поддерживающие тайм-ауты (обычные файлы), возвращают false.
		return vm.Bool(false)
	}
	d := time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond
	h.readTimeout = d
	h.writeTimeout = d
	return vm.Bool(true)
}

// --- stream_select ---

// syscallRawConn возвращает fd соединения/слушателя для select.
func syscallRawConn(c any) (int, bool) {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return 0, false
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return 0, false
	}
	fd := -1
	rc.Control(func(f uintptr) { fd = int(f) })
	if fd < 0 {
		return 0, false
	}
	return fd, true
}

// streamAlwaysReady сообщает, что поток всегда готов (обычный файл/память).
func streamAlwaysReady(h *fsHandle) (read, write bool) {
	if h.us != nil {
		return len(h.rbuf) > 0 || h.rbof, true
	}
	if h.conn != nil || h.ln != nil {
		return false, false
	}
	if h.f == nil {
		return false, false
	}
	fi, err := h.f.Stat()
	if err != nil {
		return false, false
	}
	if fi.Mode().IsRegular() || h.isMemory || h.dir {
		return true, true
	}
	return false, false
}

type selectEntry struct {
	key     vm.ArrayKey
	stream  vm.Slot
	h       *fsHandle
	ready   bool
	fd      int  // представленный fd (для пользовательских обёрток через stream_cast)
	hasFd   bool // fd задан
	invalid bool // обёртку не удалось представить как select()-дескриптор
}

// warnCannotRepresent печатает предупреждение о непредставимом потоке.
func warnCannotRepresent(ctx *vm.Context) {
	ctx.VM().Warning("stream_select(): Cannot represent a stream of type user-space as a select()able descriptor")
}

// castUserForSelect пытается получить fd для пользовательской обёртки через
// stream_cast. Возвращает (fd, ok); ok=false — поток непредставим.
func castUserForSelect(ctx *vm.Context, h *fsHandle, fn string) (int, bool) {
	o := h.us.vm.SlotObject(h.us.obj)
	name := ""
	var ci vm.ClassInfo
	if o != nil {
		name = o.ClassName()
		ci = h.us.vm.FindClass(name)
	}
	if ci == nil || !hasMethod(ci, "stream_cast") {
		ctx.VM().Warning(fmt.Sprintf("%s(): %s::stream_cast is not implemented!", fn, name))
		return 0, false
	}
	res, ok := h.us.callMethod("stream_cast", vm.MakeInt(3))
	if !ok {
		return 0, false
	}
	if res.Type() == vm.BoolType && !res.ToBool() {
		return 0, false
	}
	r := vm.AsResource(res)
	if r == nil || r.Kind != "stream" {
		ctx.VM().Warning(fmt.Sprintf("%s(): %s::stream_cast must return a stream resource", fn, name))
		return 0, false
	}
	target := fsHandleOf(res)
	if target == h {
		ctx.VM().Warning(fmt.Sprintf("%s(): %s::stream_cast must not return itself", fn, name))
		return 0, false
	}
	if target == nil {
		ctx.VM().Warning(fmt.Sprintf("%s(): %s::stream_cast must return a stream resource", fn, name))
		return 0, false
	}
	if target.us != nil {
		fd, ok2 := castUserForSelect(ctx, target, fn)
		if !ok2 {
			warnCannotRepresent(ctx)
			return 0, false
		}
		return fd, true
	}
	if fd, ok2 := streamFd(target); ok2 {
		return fd, true
	}
	warnCannotRepresent(ctx)
	return 0, false
}

func collectSelect(ctx *vm.Context, a *vm.Array, forWrite bool) []selectEntry {
	var out []selectEntry
	if a == nil {
		return out
	}
	for k, s := range a.All() {
		h := fsHandleOf(s)
		if h == nil {
			continue
		}
		e := selectEntry{key: k, stream: s, h: h}
		if h.us != nil {
			fd, ok := castUserForSelect(ctx, h, "stream_select")
			if !ok {
				warnCannotRepresent(ctx)
				e.invalid = true
				out = append(out, e)
				continue
			}
			e.fd, e.hasFd = fd, true
			out = append(out, e)
			continue
		}
		rd, wr := streamAlwaysReady(h)
		if forWrite {
			e.ready = wr
		} else {
			e.ready = rd || len(h.rbuf) > 0 || h.rbof
		}
		out = append(out, e)
	}
	return out
}

func fdSet(fd int) syscall.FdSet {
	var s syscall.FdSet
	if fd >= 0 && fd < 1024 {
		s.Bits[fd/64] |= 1 << (uint(fd) % 64)
	}
	return s
}

// waitFds ожидает готовности наборов fd; возвращает готовые fd для чтения и
// записи. Если ни одного fd нет — не ждёт (в PHP нет потоков для select).
func waitFds(readFds, writeFds []int, timeout time.Duration) (map[int]bool, map[int]bool) {
	tv := &syscall.Timeval{Sec: int64(timeout / time.Second), Usec: int64((timeout % time.Second) / time.Microsecond)}
	return waitFdsTV(readFds, writeFds, tv)
}

// waitFdsBlock — как waitFds, но ждёт без тайм-аута (seconds === null).
func waitFdsBlock(readFds, writeFds []int) (map[int]bool, map[int]bool) {
	return waitFdsTV(readFds, writeFds, nil)
}

func waitFdsTV(readFds, writeFds []int, tv *syscall.Timeval) (map[int]bool, map[int]bool) {
	rr := map[int]bool{}
	rw := map[int]bool{}
	var r, w syscall.FdSet
	maxfd := -1
	valid := 0
	add := func(fd int, set *syscall.FdSet) {
		if fd < 0 || fd >= 1024 {
			return
		}
		set.Bits[fd/64] |= 1 << (uint(fd) % 64)
		if fd > maxfd {
			maxfd = fd
		}
		valid++
	}
	for _, fd := range readFds {
		add(fd, &r)
	}
	for _, fd := range writeFds {
		add(fd, &w)
	}
	if valid == 0 {
		return rr, rw
	}
	syscall.Select(maxfd+1, &r, &w, nil, tv)
	for _, fd := range readFds {
		if fd >= 0 && fd < 1024 && r.Bits[fd/64]&(1<<(uint(fd)%64)) != 0 {
			rr[fd] = true
		}
	}
	for _, fd := range writeFds {
		if fd >= 0 && fd < 1024 && w.Bits[fd/64]&(1<<(uint(fd)%64)) != 0 {
			rw[fd] = true
		}
	}
	return rr, rw
}

// streamSelectFn — stream_select().
func streamSelectFn(ctx *vm.Context) vm.Value {
	readM, writeM := (*vm.Array)(nil), (*vm.Array)(nil)
	exceptM := (*vm.Array)(nil)
	if a := arg(ctx, 0); a.Type() == vm.ArrayType {
		readM = a.AsArray()
	}
	if a := arg(ctx, 1); a.Type() == vm.ArrayType {
		writeM = a.AsArray()
	}
	if a := arg(ctx, 2); a.Type() == vm.ArrayType {
		exceptM = a.AsArray()
	}
	secArg := arg(ctx, 3)
	if secArg.Type() == vm.FloatType {
		f := secArg.ToFloat()
		if f != math.Trunc(f) {
			ctx.VM().Deprecated("Implicit conversion from float " + phpFloat(f) + " to int loses precision")
		}
	}
	var sec int64 = -1
	if secArg.Type() != vm.NullType {
		sec = secArg.ToInt()
	}
	var usec int64
	usecSet := false
	if len(ctx.Args()) >= 5 && arg(ctx, 4).Type() != vm.NullType {
		usec = arg(ctx, 4).ToInt()
		usecSet = true
	}
	if sec < 0 && usecSet && usec > 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "stream_select(): Argument #5 ($microseconds) must be null when argument #4 ($seconds) is null"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	var timeout time.Duration
	if sec >= 0 {
		timeout = time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond
	}

	reads := collectSelect(ctx, readM, false)
	writes := collectSelect(ctx, writeM, true)

	selectable := 0
	var readFds, writeFds []int
	for i := range reads {
		e := &reads[i]
		if e.invalid {
			continue
		}
		if e.ready {
			selectable++
			continue
		}
		if e.hasFd {
			readFds = append(readFds, e.fd)
			selectable++
			continue
		}
		if fd, ok := streamFd(e.h); ok {
			readFds = append(readFds, fd)
			selectable++
		}
	}
	for i := range writes {
		e := &writes[i]
		if e.invalid {
			continue
		}
		if e.ready {
			selectable++
			continue
		}
		if e.hasFd {
			writeFds = append(writeFds, e.fd)
			selectable++
			continue
		}
		if fd, ok := streamFd(e.h); ok {
			writeFds = append(writeFds, fd)
			selectable++
		}
	}

	if (len(reads) > 0 || len(writes) > 0) && selectable == 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "No stream arrays were passed"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if len(reads) == 0 && len(writes) == 0 {
		return vm.Int(0)
	}
	var readyR, readyW map[int]bool
	// seconds === null — ожидание без тайм-аута (блокирующий select), иначе
	// тайм-аут (0 — опрос).
	if sec < 0 {
		readyR, readyW = waitFdsBlock(readFds, writeFds)
	} else {
		readyR, readyW = waitFds(readFds, writeFds, timeout)
	}

	markReady := func(entries []selectEntry, fds []int, ready map[int]bool, refIdx int, a *vm.Array) int {
		count := 0
		for i := range entries {
			e := &entries[i]
			if e.invalid || e.ready {
				continue
			}
			if e.hasFd {
				if ready[e.fd] {
					e.ready = true
				}
				continue
			}
			if fd, ok := streamFd(e.h); ok && ready[fd] {
				e.ready = true
			}
		}
		if a != nil {
			na := vm.NewArray()
			for _, e := range entries {
				if e.ready {
					na.Set(e.key, e.stream)
					count++
				}
			}
			setArrayArg(ctx, refIdx, na)
		}
		return count
	}
	n := markReady(reads, readFds, readyR, 0, readM)
	n += markReady(writes, writeFds, readyW, 1, writeM)
	if exceptM != nil {
		na := vm.NewArray()
		setArrayArg(ctx, 2, na)
	}
	return vm.Int(int64(n))
}

// streamFd возвращает fd сокета/слушателя/pipe для select.
func streamFd(h *fsHandle) (int, bool) {
	if h.conn != nil {
		return syscallRawConn(h.conn)
	}
	if h.ln != nil {
		return syscallRawConn(h.ln)
	}
	if h.f != nil {
		return int(h.f.Fd()), true
	}
	return 0, false
}

// setArrayArg записывает массив в by-ref аргумент #i.
func setArrayArg(ctx *vm.Context, i int, a *vm.Array) {
	s := arg(ctx, i)
	if s.IsRef() && s.Ref() != nil {
		s.Ref().Set(vm.MakeArray(a))
		return
	}
	if s.Type() == vm.ArrayType {
		// Не by-ref: заменяем содержимое (для безопасности).
		_ = s.AsArray()
	}
}

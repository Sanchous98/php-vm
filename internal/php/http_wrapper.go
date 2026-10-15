package php

// HTTP-обёртка потоков (http://) — минимально достаточная реализация
// поведения php_stream_url_wrap_http из ext/standard/http_fopen_wrapper.c:
// запрос, разбор ответа (включая folding-заголовки), редиректы
// (follow_location/max_redirects, сохранение метода для 307/308),
// ignore_errors, proxy/request_fulluri, $http_response_header.
//
// Тело ответа читается целиком при открытии (для тестов корпуса этого
// достаточно); поток поверх тела — обычный fsHandle с временным файлом.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"phpvm/internal/vm"
)

// httpMaxLocationSize — HTTP_HEADER_MAX_LOCATION_SIZE (8182 = 8192 - len("Location: ")).
const httpMaxLocationSize = 8182

// httpDefaultMaxRedirects — HTTP_DEFAULT_MAX_REDIRECTS.
const httpDefaultMaxRedirects = 20

// phpTimeoutMax — PHP_TIMEOUT_ULL_MAX/1e6 (предел опции timeout).
var phpTimeoutMax = float64(^uint64(0)) / 1000000.0

// isHTTPURL сообщает, адресован ли путь HTTP(S)-обёртке.
func isHTTPURL(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// httpOptions — опции контекста ['http'].
type httpOptions struct {
	method         string
	haveMethod     bool
	headers        []string
	content        string
	haveContent    bool
	followLocation bool
	haveFollow     bool
	maxRedirects   int
	ignoreErrors   bool
	protocol       string
	proxy          string
	requestFullURI bool
	timeout        time.Duration
}

// contextArgSlot возвращает контекст из аргумента ctxIdx; как
// php_stream_context_from_zval: NULL/отсутствие аргумента — контекст по
// умолчанию (создаётся лениво).
func contextArgSlot(ctx *vm.Context, idx int) *streamContext {
	ensureDefaultStreamContext()
	if len(ctx.Args()) > idx {
		v := arg(ctx, idx)
		if v.Type() == vm.ResourceType {
			if r := vm.AsResource(v); r != nil && r.Kind == "stream-context" && !r.Closed {
				if sc, ok := r.Val.(*streamContext); ok {
					return sc
				}
			}
		}
	}
	if r := vm.AsResource(streamContextDefault); r != nil {
		if sc, ok := r.Val.(*streamContext); ok {
			return sc
		}
	}
	return nil
}

// ctxWrapperOpt читает опцию context[wrap][name].
func ctxWrapperOpt(sc *streamContext, wrap, name string) (vm.Slot, bool) {
	if sc == nil || sc.options.Type() != vm.ArrayType {
		return vm.Slot{}, false
	}
	w, ok := sc.options.AsArray().Get(vm.MakeStrKey(wrap))
	if !ok || w.Type() != vm.ArrayType {
		return vm.Slot{}, false
	}
	return w.AsArray().Get(vm.MakeStrKey(name))
}

// httpContextOptions извлекает опции http-обёртки из контекста.
func httpContextOptions(sc *streamContext) httpOptions {
	o := httpOptions{
		followLocation: true,
		maxRedirects:   httpDefaultMaxRedirects,
		protocol:       "1.1",
		timeout:        60 * time.Second,
	}
	if sc == nil {
		return o
	}
	if v, ok := ctxWrapperOpt(sc, "http", "method"); ok && v.Type() == vm.StringType {
		if s := v.StringVal(); s != "" {
			o.method = s
			o.haveMethod = true
		}
	}
	if v, ok := ctxWrapperOpt(sc, "http", "header"); ok {
		switch v.Type() {
		case vm.StringType:
			o.headers = splitHeaderLines(v.StringVal())
		case vm.ArrayType:
			var b strings.Builder
			for _, e := range v.AsArray().Values() {
				if e.Type() == vm.StringType {
					b.WriteString(e.StringVal())
					b.WriteString("\r\n")
				}
			}
			o.headers = splitHeaderLines(b.String())
		}
	}
	if v, ok := ctxWrapperOpt(sc, "http", "content"); ok && v.Type() == vm.StringType {
		o.content = v.StringVal()
		o.haveContent = o.content != ""
	}
	if v, ok := ctxWrapperOpt(sc, "http", "follow_location"); ok {
		o.followLocation = v.ToBool()
		o.haveFollow = true
	}
	if v, ok := ctxWrapperOpt(sc, "http", "max_redirects"); ok {
		o.maxRedirects = int(v.ToInt())
	}
	if v, ok := ctxWrapperOpt(sc, "http", "ignore_errors"); ok {
		o.ignoreErrors = v.ToBool()
	}
	if v, ok := ctxWrapperOpt(sc, "http", "protocol_version"); ok {
		o.protocol = fmt.Sprintf("%.1f", v.ToFloat())
	}
	if v, ok := ctxWrapperOpt(sc, "http", "proxy"); ok && v.Type() == vm.StringType && v.StringVal() != "" {
		o.proxy = v.StringVal()
	}
	if v, ok := ctxWrapperOpt(sc, "http", "request_fulluri"); ok {
		o.requestFullURI = v.ToBool()
	}
	if v, ok := ctxWrapperOpt(sc, "http", "timeout"); ok {
		d := v.ToFloat()
		// timeout must be lower than ULLONG_MAX/1e6 (PHP_TIMEOUT_ULL_MAX).
		if d > phpTimeoutMax {
			o.timeout = -1
		} else if d > 0 {
			o.timeout = time.Duration(d * float64(time.Second))
		} else {
			o.timeout = 0
		}
	}
	return o
}

// splitHeaderLines нормализует значение опции header: строки без пустых
// хвостов, как php_trim в http_fopen_wrapper.c.
func splitHeaderLines(s string) []string {
	s = strings.Trim(s, " \t\r\n\x00\x0B")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\r\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimRight(p, " \t")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// httpResponse — разобранный ответ сервера.
type httpResponse struct {
	statusLine    string
	code          int
	headerLines   []string // строки для $http_response_header (статус + заголовки)
	location      string
	hasLocation   bool
	body          []byte
	contentLength int64
	chunked       bool
	// url — итоговый URL запроса (после редиректов).
	url string
	// allHeaders — накопленные строки всех ответов цепочки редиректов.
	allHeaders []string
}

// httpWrapperError — ошибка обёртки (текст идёт в "Failed to open stream: %s").
type httpWrapperError struct{ msg string }

func (e *httpWrapperError) Error() string { return e.msg }

// getHeadersFn — get_headers($url, $associative = false, $context = null):
// строки ответа всех редиректов либо ассоциативный массив имя=>значение.
func getHeadersFn(ctx *vm.Context) vm.Value {
	urlStr := arg0(ctx).ToString()
	format := len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool()
	resp, ok := httpDoRequest(ctx, urlStr, 2, true)
	if !ok {
		reason := openFailReason(urlStr, "r")
		ctx.VM().Warning("get_headers(" + urlStr + "): Failed to open stream: " + reason)
		return vm.Bool(false)
	}
	if len(resp.allHeaders) == 0 {
		return vm.Bool(false)
	}
	out := vm.NewArray()
	for _, line := range resp.allHeaders {
		if !format {
			out.Append(vm.MakeString(line))
			continue
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			// Строка без имени (статус) — как add_next_index в php-src.
			out.Append(vm.MakeString(line))
			continue
		}
		name := line[:i]
		val := strings.TrimLeft(line[i+1:], " \t")
		if prev, exists := out.Get(vm.MakeStrKey(name)); exists {
			// Повторяющиеся заголовки превращаются в массив значений.
			if prev.Type() == vm.ArrayType {
				prev.AsArray().Append(vm.MakeString(val))
			} else {
				arr := vm.NewArray()
				arr.Append(prev)
				arr.Append(vm.MakeString(val))
				out.SetStr(name, vm.MakeArray(arr))
			}
		} else {
			out.SetStr(name, vm.MakeString(val))
		}
	}
	return vm.MakeArray(out)
}

// streamContextSetDefaultFn — stream_context_set_default($options): заменяет
// опции контекста по умолчанию и возвращает его ресурс.
func streamContextSetDefaultFn(ctx *vm.Context) vm.Value {
	if arg0(ctx).Type() == vm.ArrayType && !validContextOptions(arg0(ctx).AsArray()) {
		ctx.Throw(vm.NewThrowable("ValueError", `Options should have the form ["wrappername"]["optionname"] = $value`))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	ensureDefaultStreamContext()
	if r := vm.AsResource(streamContextDefault); r != nil {
		if sc, ok := r.Val.(*streamContext); ok && arg0(ctx).Type() == vm.ArrayType {
			sc.options = deepCopySlot(arg0(ctx))
		}
	}
	return streamContextDefault
}

// httpOpenStream открывает http:// поток. ctxIdx — индекс аргумента-контекста
// в нативном вызове. При ошибке возвращает false и выставляет lastUserOpenErr.
func httpOpenStream(ctx *vm.Context, rawURL, mode string, ctxIdx int, _ string) (*fsHandle, bool) {
	if strings.ContainsAny(mode, "awx+") {
		lastUserOpenErr = "HTTP wrapper does not support writeable connections"
		return nil, false
	}
	resp, ok := httpDoRequest(ctx, rawURL, ctxIdx, false)
	if !ok {
		return nil, false
	}
	return httpFinishHandle(ctx, resp)
}

// httpDoRequest выполняет запрос с редиректами (аналог
// php_stream_url_wrap_http_ex); onlyHeaders — тело не читается
// (STREAM_ONLY_GET_HEADERS / get_headers).
func httpDoRequest(ctx *vm.Context, rawURL string, ctxIdx int, onlyHeaders bool) (*httpResponse, bool) {
	sc := contextArgSlot(ctx, ctxIdx)
	o := httpContextOptions(sc)
	if o.timeout < 0 {
		lastUserOpenErr = fmt.Sprintf("timeout must be lower than %d", uint64(phpTimeoutMax))
		return nil, false
	}
	// request_fulluri: полный URI не должен содержать CR/LF (до разбора URL).
	if o.requestFullURI && strings.ContainsAny(rawURL, "\r\n") {
		lastUserOpenErr = "HTTP wrapper full URI path does not allow CR or LF characters"
		return nil, false
	}
	cur, err := url.Parse(rawURL)
	if err != nil || cur.Host == "" {
		lastUserOpenErr = "php_network_getaddresses: getaddrinfo failed"
		return nil, false
	}
	if cur.Scheme == "https" {
		// Без openssl https-обёртка недоступна — как в сборке без SSL.
		lastUserOpenErr = "Unable to find the wrapper \"https\" - did you forget to enable it when you configured PHP?"
		return nil, false
	}

	var allHeaders []string
	redirectMax := o.maxRedirects
	redirected := false
	keepMethod := false

	for {
		if redirectMax < 1 {
			lastUserOpenErr = "Redirection limit reached, aborting"
			return nil, false
		}
		resp, rerr := httpRoundTrip(o, cur, rawURL, redirected, keepMethod, onlyHeaders)
		if rerr != nil {
			lastUserOpenErr = rerr.Error()
			return nil, false
		}
		allHeaders = append(allHeaders, resp.headerLines...)

		reqok := (resp.code >= 200 && resp.code < 400) || o.ignoreErrors
		needFollow := resp.hasLocation && o.followLocation
		if !reqok || needFollow {
			if !o.followLocation || (o.ignoreErrors && redirectMax <= 1) {
				if !reqok {
					lastUserOpenErr = "HTTP request failed! " + resp.statusLine
					return nil, false
				}
				resp.allHeaders = allHeaders
				return resp, true
			}
			if resp.hasLocation {
				newURL, lerr := httpResolveLocation(cur, resp.location)
				if lerr != nil {
					lastUserOpenErr = lerr.Error()
					return nil, false
				}
				redirectMax--
				redirected = true
				if resp.code == 307 || resp.code == 308 {
					keepMethod = true
				}
				cur = newURL
				continue
			}
			lastUserOpenErr = "HTTP request failed! " + resp.statusLine
			return nil, false
		}
		resp.allHeaders = allHeaders
		return resp, true
	}
}

// httpFinishHandle создаёт поток поверх прочитанного тела и публикует
// $http_response_header.
func httpFinishHandle(ctx *vm.Context, resp *httpResponse) (*fsHandle, bool) {
	f, err := os.CreateTemp("", "php-http")
	if err != nil {
		lastUserOpenErr = "operation failed"
		return nil, false
	}
	if len(resp.body) > 0 {
		if _, werr := f.Write(resp.body); werr != nil {
			f.Close()
			lastUserOpenErr = "operation failed"
			return nil, false
		}
		if _, serr := f.Seek(0, io.SeekStart); serr != nil {
			f.Close()
			lastUserOpenErr = "operation failed"
			return nil, false
		}
	}
	h := &fsHandle{f: f, path: resp.url, mode: "r", push: -1, tmp: true,
		canRead: true, isSocket: true, sockType: "tcp_socket/ssl"}
	arr := vm.NewArray()
	for _, line := range resp.allHeaders {
		arr.Append(vm.MakeString(line))
	}
	h.meta = map[string]vm.Slot{
		"wrapper_type": vm.MakeString("http"),
		"stream_type":  vm.MakeString("tcp_socket/ssl"),
		"wrapper_data": vm.MakeArray(arr),
	}
	_ = ctx.VM().SymtabSet("http_response_header", vm.MakeArray(arr))
	return h, true
}

// httpRoundTrip выполняет один запрос (с учётом proxy/request_fulluri).
func httpRoundTrip(o httpOptions, u *url.URL, rawURL string, redirected, keepMethod, onlyHeaders bool) (*httpResponse, error) {
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	hostPort := u.Host
	hostOnly := u.Hostname()
	port := u.Port()
	dialAddr := hostPort
	if port == "" {
		port = "80"
		dialAddr = net.JoinHostPort(hostOnly, port)
	}
	if o.requestFullURI {
		target = rawURL
	}
	transport := dialAddr
	if o.proxy != "" {
		transport = o.proxy
	}
	dialNetwork, dialTarget, derr := parseTransport(transport)
	if derr != nil {
		return nil, &httpWrapperError{msg: derr.Error()}
	}

	// Метод: при редиректе без keepMethod не-GET/HEAD превращается в GET.
	method := o.method
	useMethod := o.haveMethod
	if !useMethod {
		method = "GET"
	}
	if redirected && !keepMethod && method != "GET" && method != "HEAD" {
		method = "GET"
		useMethod = false
	}
	sendContent := o.haveContent && (!redirected || keepMethod)
	// Content-Length/Content-Type пользователя снимаются при редиректе
	// без сохранения метода.
	userHeaders := o.headers
	if redirected && !keepMethod {
		userHeaders = stripHeaders(userHeaders, "content-length", "content-type")
	}
	haveContentLength, haveContentType := false, false
	haveHost, haveConnection := false, false
	for _, h := range userHeaders {
		name := strings.ToLower(headerName(h))
		switch name {
		case "content-length":
			haveContentLength = true
		case "content-type":
			haveContentType = true
		case "host":
			haveHost = true
		case "connection":
			haveConnection = true
		}
	}

	var req bytes.Buffer
	fmt.Fprintf(&req, "%s %s HTTP/%s\r\n", method, target, o.protocol)
	for _, h := range userHeaders {
		req.WriteString(h)
		req.WriteString("\r\n")
	}
	if !haveHost {
		fmt.Fprintf(&req, "Host: %s\r\n", hostPort)
	}
	if !haveConnection {
		req.WriteString("Connection: close\r\n")
	}
	if sendContent {
		if !haveContentLength {
			fmt.Fprintf(&req, "Content-Length: %d\r\n", len(o.content))
		}
		if !haveContentType {
			req.WriteString("Content-Type: application/x-www-form-urlencoded\r\n")
		}
	}
	req.WriteString("\r\n")
	if sendContent {
		req.WriteString(o.content)
	}

	timeout := o.timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	conn, err := net.DialTimeout(dialNetwork, dialTarget, timeout)
	if err != nil {
		msg := dialErrText(err)
		return nil, &httpWrapperError{msg: msg}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, werr := conn.Write(req.Bytes()); werr != nil {
		return nil, &httpWrapperError{msg: dialErrText(werr)}
	}
	br := bufio.NewReader(conn)
	// Тело не читаем для HEAD и STREAM_ONLY_GET_HEADERS (в ответе на HEAD тела
	// нет, а Content-Length указывает на гипотетическое тело GET).
	skipBody := onlyHeaders || method == "HEAD"
	resp, rerr := httpParseResponse(br, skipBody)
	if rerr != nil {
		return nil, rerr
	}
	resp.url = u.String()
	return resp, nil
}

// httpParseResponse читает статус-строку и заголовки, затем тело.
func httpParseResponse(br *bufio.Reader, skipBody bool) (*httpResponse, error) {
	resp := &httpResponse{contentLength: -1}
	line, ok := httpReadLine(br)
	if !ok {
		return nil, &httpWrapperError{msg: "HTTP request failed!"}
	}
	resp.statusLine = strings.TrimRight(line, " \t")
	if len(resp.statusLine) > 9 {
		resp.code, _ = strconv.Atoi(strings.TrimSpace(resp.statusLine[9:13]))
	}
	resp.headerLines = append(resp.headerLines, resp.statusLine)
	if resp.code == 0 {
		return nil, &httpWrapperError{msg: "HTTP request failed! " + resp.statusLine}
	}

	var pending string
	havePending := false
	finishPending := func() error {
		if !havePending {
			return nil
		}
		havePending = false
		p := pending
		pending = ""
		colon := strings.IndexByte(p, ':')
		if colon < 0 {
			return &httpWrapperError{msg: "HTTP invalid response format (no colon in header line)!"}
		}
		for i := 1; i < colon; i++ {
			if p[i] == ' ' || p[i] == '\t' {
				return &httpWrapperError{msg: "HTTP invalid response format (space in header name)!"}
			}
		}
		val := strings.TrimLeft(p[colon+1:], " \t")
		resp.headerLines = append(resp.headerLines, p)
		name := strings.ToLower(p[:colon+1])
		switch name {
		case "location:":
			if len(val) > httpMaxLocationSize {
				return &httpWrapperError{msg: fmt.Sprintf("HTTP Location header size is over the limit of %d bytes", httpMaxLocationSize)}
			}
			resp.location = val
			resp.hasLocation = true
		case "content-length:":
			if v, err := strconv.ParseUint(val, 10, 63); err == nil {
				resp.contentLength = int64(v)
			}
		case "transfer-encoding:":
			if strings.HasPrefix(strings.ToLower(val), "chunked") {
				resp.chunked = true
			}
		}
		return nil
	}

	for {
		raw, ok := httpReadLineRaw(br)
		if !ok {
			// Соединение закрылось до пустой строки — обрабатываем хвост.
			if err := finishPending(); err != nil {
				return nil, err
			}
			break
		}
		if raw == "\r\n" || raw == "\n" || raw == "" {
			if err := finishPending(); err != nil {
				return nil, err
			}
			break
		}
		if raw[0] == '\r' {
			if len(raw) < 2 || raw[1] != '\n' {
				return nil, &httpWrapperError{msg: "HTTP invalid header name (cannot start with CR character)!"}
			}
			if err := finishPending(); err != nil {
				return nil, err
			}
			break
		}
		line := trimHeaderLine(raw)
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if !havePending {
				return nil, &httpWrapperError{msg: "HTTP invalid response format (folding header at the start)!"}
			}
			pending += " " + strings.TrimLeft(line, " \t")
			continue
		}
		if err := finishPending(); err != nil {
			return nil, err
		}
		pending = line
		havePending = true
	}

	if skipBody {
		return resp, nil
	}
	// Тело: chunked / Content-Length / до EOF.
	var body []byte
	var err error
	switch {
	case resp.chunked:
		body, err = httpReadChunked(br)
	case resp.contentLength >= 0:
		body = make([]byte, resp.contentLength)
		_, err = io.ReadFull(br, body)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			err = nil
		}
	default:
		body, err = io.ReadAll(br)
	}
	if err != nil {
		return nil, &httpWrapperError{msg: "HTTP request failed!"}
	}
	resp.body = body
	return resp, nil
}

// httpReadLineRaw читает строку до \n включительно (сохраняя CR).
func httpReadLineRaw(br *bufio.Reader) (string, bool) {
	s, err := br.ReadString('\n')
	if err != nil {
		if len(s) == 0 {
			return "", false
		}
	}
	return s, true
}

// httpReadLine — сырая строка без завершающих CR/LF.
func httpReadLine(br *bufio.Reader) (string, bool) {
	s, ok := httpReadLineRaw(br)
	if !ok {
		return "", false
	}
	return strings.TrimRight(s, "\r\n"), true
}

// trimHeaderLine повторяет php_stream_http_response_header_trim: срезает
// завершающие CR/LF и пробельные символы.
func trimHeaderLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	return strings.TrimRight(s, " \t")
}

// httpReadChunked декодирует chunked-тело.
func httpReadChunked(br *bufio.Reader) ([]byte, error) {
	var out bytes.Buffer
	for {
		line, ok := httpReadLine(br)
		if !ok {
			return out.Bytes(), nil
		}
		if i := strings.IndexByte(line, ';'); i >= 0 {
			line = line[:i]
		}
		n, err := strconv.ParseInt(strings.TrimSpace(line), 16, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad chunk")
		}
		if n == 0 {
			// Завершающие заголовки чанка (trailers) пропускаем.
			for {
				l, ok := httpReadLine(br)
				if !ok || l == "" {
					break
				}
			}
			return out.Bytes(), nil
		}
		if _, err := io.CopyN(&out, br, n); err != nil {
			return nil, err
		}
		_, _ = httpReadLine(br)
	}
}

// httpResolveLocation строит URL следующего запроса (как php-src).
func httpResolveLocation(cur *url.URL, location string) (*url.URL, error) {
	if location == "" {
		return nil, &httpWrapperError{msg: "Invalid redirect URL!"}
	}
	lower := strings.ToLower(location)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		nu, err := url.Parse(location)
		if err != nil {
			return nil, &httpWrapperError{msg: "Invalid redirect URL! " + location}
		}
		if hasCtrl(nu.User) || hasCtrlStr(nu.Path) {
			return nil, &httpWrapperError{msg: "Invalid redirect URL! " + location}
		}
		return nu, nil
	}
	ref, err := url.Parse(location)
	if err != nil {
		return nil, &httpWrapperError{msg: "Invalid redirect URL! " + location}
	}
	nu := cur.ResolveReference(ref)
	if hasCtrlStr(nu.Path) {
		return nil, &httpWrapperError{msg: "Invalid redirect URL! " + location}
	}
	return nu, nil
}

func hasCtrl(u *url.Userinfo) bool {
	if u == nil {
		return false
	}
	return hasCtrlStr(u.String())
}

func hasCtrlStr(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// headerName возвращает имя заголовка (до ':').
func headerName(h string) string {
	if i := strings.IndexByte(h, ':'); i >= 0 {
		return strings.TrimSpace(h[:i])
	}
	return strings.TrimSpace(h)
}

// stripHeaders удаляет пользовательские заголовки с указанными именами.
func stripHeaders(lines []string, names ...string) []string {
	bad := map[string]bool{}
	for _, n := range names {
		bad[n] = true
	}
	out := lines[:0:0]
	for _, h := range lines {
		if bad[strings.ToLower(headerName(h))] {
			continue
		}
		out = append(out, h)
	}
	return out
}

// parseTransport разбирает транспортную строку ("tcp://host:port",
// "ssl://host:port", "unix:///path") для net.Dial.
func parseTransport(t string) (network, addr string, err error) {
	switch {
	case strings.HasPrefix(t, "tcp://"):
		return "tcp", t[len("tcp://"):], nil
	case strings.HasPrefix(t, "ssl://"), strings.HasPrefix(t, "tls://"):
		if strings.HasPrefix(t, "ssl://") {
			return "tcp", t[len("ssl://"):], nil
		}
		return "tcp", t[len("tls://"):], nil
	case strings.HasPrefix(t, "unix://"):
		return "unix", t[len("unix://"):], nil
	}
	if strings.Contains(t, ":") {
		return "tcp", t, nil
	}
	return "", "", &httpWrapperError{msg: "Unable to connect to " + t}
}

// dialErrText приводит ошибку соединения к тексту, близкому к php-src.
func dialErrText(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "Connection refused"
	case strings.Contains(s, "no such host"), strings.Contains(s, "lookup"):
		return "php_network_getaddresses: getaddrinfo failed"
	case strings.Contains(s, "i/o timeout"), strings.Contains(s, "deadline exceeded"):
		return "Connection timed out"
	}
	return s
}

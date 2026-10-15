package php

// URL-Rewriter (ext/standard/url_scanner_ex.re): перезапись ссылок и вставка
// hidden-полей в формы при output_add_rewrite_var() и trans-sid сессиях.
//
// Модель php-src: на каждый «канал» (output — url_rewriter.*, session —
// session.trans_sid_*) заводится внутренний буфер вывода с обработчиком-
// сканером. Данные проходят через верхний буфер: сессионный сканер (если
// активен) добавляет SID, затем output-сканер добавляет переменные
// url_rewriter'а. Состояние сканера (незавершённый тег, attr_val формы)
// сохраняется между вызовами обработчика, а параметры url_app/form_app
// читаются в момент сброса буфера — как в php-src.

import (
	crand "crypto/rand"
	"net/url"
	"strings"

	"phpvm/internal/vm"
)

const obInternalStdFlags = 16 | 32 | 64 // PHP_OUTPUT_HANDLER_STDFLAGS

// urlRewriteState — состояние одного канала сканера.
type urlRewriteState struct {
	active  bool
	buf     []byte
	urlApp  string
	formApp string
	hosts   []string
	// attrVal — значение последнего action формы (php-src не сбрасывает его
	// для форм без action: унаследованное значение влияет на host-фильтр).
	attrVal string
}

var urlRewriteOut urlRewriteState
var urlRewriteSess urlRewriteState

// urlRewriteResetAll сбрасывает состояние между прогонами .phpt (харнесс).
func urlRewriteResetAll() {
	urlRewriteOut = urlRewriteState{}
	urlRewriteSess = urlRewriteState{}
}

// urlRewriteTags разбирает INI-список тегов ("a=href,form=").
func urlRewriteTags(session bool) map[string]string {
	v := iniValues["url_rewriter.tags"]
	if session {
		v = iniValues["session.trans_sid_tags"]
	}
	out := map[string]string{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, attr, _ := strings.Cut(part, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(attr)
	}
	return out
}

func urlRewriteParseHosts(v string) []string {
	var out []string
	for _, h := range strings.Split(v, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			out = append(out, strings.ToLower(h))
		}
	}
	return out
}

// urlRewriteHostAllowed — фильтр хостов для ссылок: схема только http/https,
// относительные URL разрешены, хост должен быть в списке (пустой список не
// разрешает хостовые URL).
func urlRewriteHostAllowed(rawURL string, hosts []string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return true
	}
	for _, h := range hosts {
		if h == host {
			return true
		}
	}
	return false
}

// urlRewriteFormAllowed — check_host_whitelist: пустой attr_val всегда
// проходит; хост-URL проходит при пустом списке только если совпал с
// $_SERVER['HTTP_HOST'] (в харнессе не задан — как в php-src в CLI).
func urlRewriteFormAllowed(attrVal string, hosts []string) bool {
	if attrVal == "" {
		return true
	}
	u, err := url.Parse(attrVal)
	if err != nil {
		return false
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return true
	}
	if len(hosts) == 0 {
		return false
	}
	for _, h := range hosts {
		if h == host {
			return true
		}
	}
	return false
}

// urlRewriteLink добавляет url_app к ссылке (append_modified_url).
func urlRewriteLink(raw, app string) string {
	if app == "" {
		return raw
	}
	if strings.HasPrefix(raw, "#") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return raw
	}
	base, frag := raw, ""
	if i := strings.IndexByte(base, '#'); i >= 0 {
		frag = base[i:]
		base = base[:i]
	}
	if base == "" {
		return "?" + app + frag
	}
	if i := strings.IndexByte(base, '?'); i >= 0 {
		if base[i+1:] == "" {
			return base + app + frag
		}
		return base + "&" + app + frag
	}
	return base + "?" + app + frag
}

// urlRewriteEscape повторяет htmlspecialchars(ENT_QUOTES) для hidden-полей.
func urlRewriteEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#039;")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// urlRewriteAddVar добавляет переменную в состояние канала: url-приложение и
// hidden-поле формы.
func urlRewriteAddVar(st *urlRewriteState, name, value string) {
	if st.urlApp != "" {
		st.urlApp += "&"
	}
	st.urlApp += phpRawURLEncode(name) + "=" + phpRawURLEncode(value)
	st.formApp += `<input type="hidden" name="` + urlRewriteEscape(name) + `" value="` + urlRewriteEscape(value) + `" />`
}

// urlRewriteScan обрабатывает данные сканером. final (FLUSH/FINAL) сбрасывает
// и незавершённый хвост.
func urlRewriteScan(st *urlRewriteState, data []byte, final bool) []byte {
	st.buf = append(st.buf, data...)
	src := st.buf
	var out []byte
	i := 0
	for i < len(src) {
		if src[i] != '<' {
			j := strings.IndexByte(string(src[i:]), '<')
			if j < 0 {
				if final {
					out = append(out, src[i:]...)
					i = len(src)
				}
				break
			}
			out = append(out, src[i:i+j]...)
			i += j
			continue
		}
		next, ok := urlRewriteTag(st, src, i, &out, final)
		if !ok {
			break // незавершённый тег — остаётся в st.buf
		}
		i = next
	}
	if i >= len(src) {
		st.buf = st.buf[:0]
	} else if final {
		out = append(out, src[i:]...)
		st.buf = st.buf[:0]
	} else {
		st.buf = append(st.buf[:0], src[i:]...)
	}
	if final {
		st.attrVal = ""
	}
	return out
}

// urlRewriteTag обрабатывает один тег начиная с src[i]=='<'. ok=false —
// тег не завершён (данные нужно дождаться).
func urlRewriteTag(st *urlRewriteState, src []byte, i int, out *[]byte, final bool) (int, bool) {
	// Комментарий: <!-- ... -->
	if i+3 < len(src) && src[i+1] == '!' {
		if i+4 <= len(src) && string(src[i:i+4]) == "<!--" {
			end := strings.Index(string(src[i:]), "-->")
			if end < 0 {
				if final {
					*out = append(*out, src[i:]...)
					return len(src), true
				}
				return i, false
			}
			*out = append(*out, src[i:i+end+3]...)
			return i + end + 3, true
		}
	}
	// Имя тега после '<' (и, возможно, '/').
	p := i + 1
	if p < len(src) && src[p] == '/' {
		p++
	}
	nameStart := p
	for p < len(src) && isTagNameByte(src[p]) {
		p++
	}
	if p == nameStart {
		// Не тег — одиночный '<'.
		*out = append(*out, src[i])
		return i + 1, true
	}
	name := strings.ToLower(string(src[nameStart:p]))
	if i+1 < len(src) && src[i+1] == '/' {
		// Закрывающий тег (</form>) — не обрабатывается сканером.
		return urlRewritePassthruTag(src, i, out, final)
	}
	if p < len(src) && !isURLSpaceByte(src[p]) && src[p] != '>' && src[p] != '/' {
		// <a1 или подобное — не наш тег; копируем как есть до '>'.
		return urlRewritePassthruTag(src, i, out, final)
	}
	rules := urlRewriteTags(st.session())
	attrName, interesting := rules[name]
	if !interesting {
		return urlRewritePassthruTag(src, i, out, final)
	}
	if name == "form" && attrName == "" {
		// Форма: нужен только action (для фильтра) — и вставка hidden-полей.
	} else if attrName == "" {
		return urlRewritePassthruTag(src, i, out, final)
	}

	// Разбор атрибутов до '>'.
	end, ok := urlRewriteTagEnd(src, p)
	if !ok {
		if final {
			*out = append(*out, src[i:]...)
			return len(src), true
		}
		return i, false
	}
	// Атрибуты: ищем целевой атрибут (href/src/action).
	tagText := src[i : end+1]
	targetVal := ""
	targetStart, targetEnd := -1, -1
	j := p
	for j < end {
		for j < end && (isURLSpaceByte(src[j]) || src[j] == '/') {
			j++
		}
		if j >= end {
			break
		}
		as := j
		for j < end && !isURLSpaceByte(src[j]) && src[j] != '=' && src[j] != '>' {
			j++
		}
		aName := strings.ToLower(string(src[as:j]))
		for j < end && isURLSpaceByte(src[j]) {
			j++
		}
		if j < end && src[j] == '=' {
			j++
			for j < end && isURLSpaceByte(src[j]) {
				j++
			}
			if j < end && (src[j] == '"' || src[j] == '\'') {
				q := src[j]
				j++
				vs := j
				for j < end && src[j] != q {
					j++
				}
				if j < end {
					if aName == attrName {
						targetVal = string(src[vs:j])
						targetStart, targetEnd = vs, j
					}
					if name == "form" && aName == "action" {
						st.attrVal = string(src[vs:j])
					}
					j++
				}
			} else {
				vs := j
				for j < end && !isURLSpaceByte(src[j]) {
					j++
				}
				if aName == attrName {
					targetVal = string(src[vs:j])
					targetStart, targetEnd = vs, j
				}
				if name == "form" && aName == "action" {
					st.attrVal = string(src[vs:j])
				}
			}
		}
	}

	switch {
	case name == "form":
		*out = append(*out, tagText...)
		if st.formApp != "" && urlRewriteFormAllowed(st.attrVal, st.hosts) {
			*out = append(*out, st.formApp...)
		}
	default:
		if targetStart >= 0 && st.urlApp != "" && urlRewriteHostAllowed(targetVal, st.hosts) {
			newVal := urlRewriteLink(targetVal, st.urlApp)
			// targetStart/targetEnd — границы значения без кавычек; префикс
			// уже содержит открывающую кавычку, суффикс — закрывающую.
			*out = append(*out, src[i:targetStart]...)
			*out = append(*out, newVal...)
			*out = append(*out, src[targetEnd:end+1]...)
		} else {
			*out = append(*out, tagText...)
		}
	}
	return end + 1, true
}

// urlRewritePassthruTag копирует тег целиком до '>' (или до конца при final).
func urlRewritePassthruTag(src []byte, i int, out *[]byte, final bool) (int, bool) {
	end, ok := urlRewriteTagEnd(src, i+1)
	if !ok {
		if final {
			*out = append(*out, src[i:]...)
			return len(src), true
		}
		return i, false
	}
	*out = append(*out, src[i:end+1]...)
	return end + 1, true
}

// urlRewriteTagEnd ищет '>' с учётом кавычек.
func urlRewriteTagEnd(src []byte, from int) (int, bool) {
	var q byte
	for j := from; j < len(src); j++ {
		c := src[j]
		if q != 0 {
			if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			q = c
		case '>':
			return j, true
		}
	}
	return 0, false
}

func isTagNameByte(c byte) bool {
	return c == ':' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isURLSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// session сообщает, что состояние — сессионный канал (для выбора INI-тегов).
func (st *urlRewriteState) session() bool { return st == &urlRewriteSess }

// --- обработчики вывода ---

func urlRewriterOutFn(ctx *vm.Context) vm.Value  { return urlRewriterHandle(ctx, &urlRewriteOut) }
func urlRewriterSessFn(ctx *vm.Context) vm.Value { return urlRewriterHandle(ctx, &urlRewriteSess) }

func urlRewriterHandle(ctx *vm.Context, st *urlRewriteState) vm.Value {
	data := arg0(ctx).ToString()
	flags := arg(ctx, 1).ToInt()
	if flags&2 != 0 { // CLEAN: буфер уничтожается
		st.buf = st.buf[:0]
		st.attrVal = ""
		return vm.Str("")
	}
	final := flags&8 != 0 || flags&4 != 0
	out := urlRewriteScan(st, []byte(data), final)
	return vm.Str(string(out))
}

// urlRewriterActivate запускает буфер сканера указанного канала.
func urlRewriterActivate(ctx *vm.Context, session bool) {
	st := &urlRewriteOut
	name := "__php_url_rewriter_out"
	hostsIni := "url_rewriter.hosts"
	if session {
		st = &urlRewriteSess
		name = "__php_url_rewriter_sess"
		hostsIni = "session.trans_sid_hosts"
	}
	if st.active {
		return
	}
	st.active = true
	st.hosts = urlRewriteParseHosts(iniValues[hostsIni])
	ctx.VM().ObStart(vm.MakeString(name), 0, obInternalStdFlags)
}

// urlRewriterDeactivate сбрасывает буфер канала (session_commit).
func urlRewriterDeactivate(ctx *vm.Context, session bool) {
	st := &urlRewriteOut
	if session {
		st = &urlRewriteSess
	}
	if !st.active {
		return
	}
	st.active = false
	st.buf = st.buf[:0]
	st.urlApp = ""
	st.formApp = ""
	st.attrVal = ""
	if ctx.VM().ObGetLevel() > 0 {
		ctx.VM().ObEndFlush()
	}
}

// phpSessionGenerateID повторяет генерацию SID php-src (26 символов a-z0-9-).
func phpSessionGenerateID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789,-"
	var raw [26]byte
	if _, err := crand.Read(raw[:]); err != nil {
		return strings.Repeat("a", 26)
	}
	var b strings.Builder
	for i := 0; i < 26; i++ {
		b.WriteByte(alphabet[int(raw[i])%len(alphabet)])
	}
	return b.String()
}

package php

import (
	"strings"
	"time"

	"phpvm/internal/vm"
)

// Файл содержит setcookie/setrawcookie/headers_list (порт ext/standard/head.c).

// Состояние заголовков: CLI PHP не хранит заголовки, но тесты ext/standard
// (--EXPECTHEADERS--/headers_list) ожидают SAPI-подобное поведение, поэтому
// Set-Cookie сохраняются, а headers_list показывает X-Powered-By, когда
// setcookie/setrawcookie вызывались.
var (
	phpCookieHeaders []string
	phpCookieCalled  bool
)

// resetCookieState очищает состояние заголовков между прогонами.
func resetCookieState() {
	phpCookieHeaders = nil
	phpCookieCalled = false
}

const illegalCookieChars = `",", ";", " ", "\t", "\r", "\n", "\013", or "\014"`

// phpRawURLEncode — php_raw_url_encode: alnum и -_. не кодируются.
func phpRawURLEncode(s string) string {
	hex := "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			c == '-' || c == '_' || c == '.' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

func hasIllegalCookieChar(s string) bool {
	return strings.ContainsAny(s, ",; \t\r\n\v\f")
}

// buildSetCookieHeader проверяет аргументы и собирает заголовок Set-Cookie.
// ok=false — исключение уже поднято; hdr пуст, если валидация не прошла.
func buildSetCookieHeader(ctx *vm.Context, fn string, name, value string, expires int64, path, domain string, secure, httponly bool, samesite string, urlEncode bool) (string, bool) {
	if name == "" {
		ctx.Throw(vm.NewThrowable("ValueError", fn+"(): Argument #1 ($name) cannot be empty"))
		return "", false
	}
	if strings.ContainsAny(name, "=") || hasIllegalCookieChar(name) {
		ctx.Throw(vm.NewThrowable("ValueError",
			fn+`(): Argument #1 ($name) cannot contain "=", `+illegalCookieChars))
		return "", false
	}
	if !urlEncode && hasIllegalCookieChar(value) {
		ctx.Throw(vm.NewThrowable("ValueError",
			fn+"(): Argument #2 ($value) cannot contain "+illegalCookieChars))
		return "", false
	}
	if hasIllegalCookieChar(path) {
		ctx.Throw(vm.NewThrowable("ValueError",
			fn+`(): "path" option cannot contain `+illegalCookieChars))
		return "", false
	}
	if hasIllegalCookieChar(domain) {
		ctx.Throw(vm.NewThrowable("ValueError",
			fn+`(): "domain" option cannot contain `+illegalCookieChars))
		return "", false
	}
	if expires >= 253402300800 {
		ctx.Throw(vm.NewThrowable("ValueError",
			fn+`(): "expires" option cannot have a year greater than 9999`))
		return "", false
	}

	var b strings.Builder
	b.WriteString("Set-Cookie: ")
	b.WriteString(name)
	if value == "" {
		b.WriteString("=deleted; expires=")
		b.WriteString(cookieDate(1))
		b.WriteString("; Max-Age=0")
	} else {
		b.WriteByte('=')
		if urlEncode {
			b.WriteString(phpRawURLEncode(value))
		} else {
			b.WriteString(value)
		}
		if expires > 0 {
			b.WriteString("; expires=")
			b.WriteString(cookieDate(expires))
			diff := expires - time.Now().Unix()
			if diff < 0 {
				diff = 0
			}
			b.WriteString("; Max-Age=")
			b.WriteString(strconvItoa64(diff))
		}
	}
	if path != "" {
		b.WriteString("; path=")
		b.WriteString(path)
	}
	if domain != "" {
		b.WriteString("; domain=")
		b.WriteString(domain)
	}
	if secure {
		b.WriteString("; secure")
	}
	if httponly {
		b.WriteString("; HttpOnly")
	}
	if samesite != "" {
		b.WriteString("; SameSite=")
		b.WriteString(samesite)
	}
	return b.String(), true
}

func strconvItoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// cookieDate форматирует время как "D, d M Y H:i:s GMT" (UTC).
func cookieDate(unix int64) string {
	return time.Unix(unix, 0).UTC().Format("Mon, 02 Jan 2006 15:04:05") + " GMT"
}

// setcookieCommon — setcookie()/setrawcookie().
func setcookieCommon(ctx *vm.Context, raw bool) vm.Value {
	fn := "setcookie"
	if raw {
		fn = "setrawcookie"
	}
	phpCookieCalled = true
	name, ok := strParam(ctx, 0, fn, "name")
	if !ok {
		return *new(vm.Value)
	}
	value := ""
	if len(ctx.Args()) >= 2 && derefSlot(arg(ctx, 1)).Type() != vm.NullType {
		value = derefSlot(arg(ctx, 1)).ToString()
	}
	expires := int64(0)
	path, domain, samesite := "", "", ""
	secure, httponly := false, false
	if len(ctx.Args()) >= 3 {
		a3 := derefSlot(arg(ctx, 2))
		if a3.Type() == vm.ArrayType {
			if len(ctx.Args()) > 3 {
				ctx.Throw(vm.NewThrowable("ArgumentCountError",
					fn+"(): Expects exactly 3 arguments when argument #3 ($expires_or_options) is an array"))
				return *new(vm.Value)
			}
			arr := a3.AsArray()
			for k, v := range arr.All() {
				if !k.IsStr() {
					ctx.Throw(vm.NewThrowable("ValueError", fn+"(): option array cannot have numeric keys"))
					return *new(vm.Value)
				}
				key := strings.ToLower(k.StrVal())
				switch key {
				case "expires":
					expires = v.ToInt()
				case "path":
					path = v.ToString()
				case "domain":
					domain = v.ToString()
				case "secure":
					secure = v.ToBool()
				case "httponly":
					httponly = v.ToBool()
				case "samesite":
					samesite = v.ToString()
				default:
					ctx.Throw(vm.NewThrowable("ValueError", fn+"(): option \""+k.StrVal()+"\" is invalid"))
					return *new(vm.Value)
				}
			}
		} else {
			expires = a3.ToInt()
			if a3.Type() == vm.NullType {
				ctx.VM().Deprecated(fn + "(): Passing null to parameter #3 ($expires_or_options) of type array|int is deprecated")
			}
			if len(ctx.Args()) >= 4 {
				path = derefSlot(arg(ctx, 3)).ToString()
			}
			if len(ctx.Args()) >= 5 {
				domain = derefSlot(arg(ctx, 4)).ToString()
			}
			if len(ctx.Args()) >= 6 {
				secure = derefSlot(arg(ctx, 5)).ToBool()
			}
			if len(ctx.Args()) >= 7 {
				httponly = derefSlot(arg(ctx, 6)).ToBool()
			}
		}
	}
	hdr, ok := buildSetCookieHeader(ctx, fn, name, value, expires, path, domain, secure, httponly, samesite, !raw)
	if !ok {
		return vm.Bool(false)
	}
	if sent, file, line := ctx.VM().HeadersSent(); sent {
		ctx.VM().Warning("Cannot modify header information - headers already sent by (output started at " +
			file + ":" + strconvItoa64(int64(line)) + ")")
		return vm.Bool(false)
	}
	phpCookieHeaders = append(phpCookieHeaders, hdr)
	return vm.Bool(true)
}

func setcookieFn(ctx *vm.Context) vm.Value    { return setcookieCommon(ctx, false) }
func setrawcookieFn(ctx *vm.Context) vm.Value { return setcookieCommon(ctx, true) }

// headersListFn — headers_list(): X-Powered-By + сохранённые Set-Cookie.
func headersListFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	if !phpCookieCalled {
		return vm.MakeArray(out)
	}
	out.Append(vm.MakeString("X-Powered-By: PHP/" + phpVersionString()))
	for _, h := range phpCookieHeaders {
		out.Append(vm.MakeString(h))
	}
	return vm.MakeArray(out)
}

// phpVersionString возвращает PHP_VERSION виртуальной машины.
func phpVersionString() string { return "8.3.6" }

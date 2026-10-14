package php

// builtins_mail.go — mail() (ext/standard/mail.c, PHP 8.3): построение письма,
// валидация дополнительных заголовков, mail.log/mail.add_x_header, запуск
// sendmail_path через popen("w").

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"phpvm/internal/vm"
)

// cstrDisplay имитирует печать C-строки (%s): строка обрывается на NUL
// (Zend печатает ZSTR_VAL без учёта длины).
func cstrDisplay(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 {
		return s[:i]
	}
	return s
}

func mailFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 3 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("mail() expects at least 3 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	to := args[0].ToString()
	subject := args[1].ToString()
	message := args[2].ToString()
	// Z_PARAM_PATH: NUL-байты отвергаются на этапе разбора аргументов.
	for i, nm := range []string{"to", "subject", "message"} {
		if strings.IndexByte([]string{to, subject, message}[i], 0) >= 0 {
			ctx.Throw(vm.NewThrowable("ValueError",
				fmt.Sprintf("mail(): Argument #%d ($%s) must not contain any null bytes", i+1, nm)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
	}

	var headersPtr *string
	var headersStr string
	if len(args) >= 4 {
		h := args[3]
		switch h.Type() {
		case vm.ArrayType:
			s, ok := phpMailBuildHeaders(ctx, h.AsArray())
			if !ok {
				return *new(vm.Value)
			}
			headersStr = s
			headersPtr = &headersStr
		case vm.NullType:
			// null = без дополнительных заголовков
		default:
			s := h.ToString()
			if strings.IndexByte(s, 0) >= 0 {
				ctx.Throw(vm.NewThrowable("ValueError",
					"mail(): Argument #4 ($additional_headers) must not contain any null bytes"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Bool(false)
			}
			s = mailTrimRight(s, " \n\r\t\v\x00")
			headersStr = s
			headersPtr = &headersStr
		}
	}

	var extraCmd string
	if len(args) >= 5 {
		extraCmd = args[4].ToString()
		if strings.IndexByte(extraCmd, 0) >= 0 {
			ctx.Throw(vm.NewThrowable("ValueError",
				"mail(): Argument #5 ($additional_params) must not contain any null bytes"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
	}

	to = mailSanitizeHeader(to)
	subject = mailSanitizeHeader(subject)

	// mail.log: запись до отправки письма.
	if logName, _ := iniValues["mail.log"]; logName != "" {
		f := ctx.VM().CurrentFile()
		line := ctx.VM().CurrentLine()
		hdrForLog := ""
		if headersPtr != nil {
			hdrForLog = *headersPtr
		}
		logline := fmt.Sprintf("mail() on [%s:%d]: To: %s -- Headers: %s -- Subject: %s", f, line, to, hdrForLog, subject)
		if headersPtr != nil {
			logline = strings.NewReplacer("\r", " ", "\n", " ").Replace(logline)
		}
		if logName == "syslog" {
			// syslog недоступен — молча пропускаем.
		} else {
			_, name := vm.DefaultTimezone()
			dateStr := vm.FormatDatePHP("d-M-Y H:i:s e", time.Now(), name)
			entry := "[" + dateStr + "] " + logline + "\n"
			fh, err := os.OpenFile(logName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				ctx.VM().Warning("mail(" + logName + "): Failed to open stream: Permission denied")
			} else {
				_, _ = fh.WriteString(entry)
				_ = fh.Close()
			}
		}
	}

	// mail.add_x_header: X-PHP-Originating-Script: uid:file
	if iniEnabled("mail.add_x_header") {
		base := mailBasename(ctx.VM().CurrentFile())
		uid := os.Getuid()
		x := fmt.Sprintf("X-PHP-Originating-Script: %d:%s", uid, base)
		if headersPtr != nil && *headersPtr != "" {
			x = x + "\r\n" + *headersPtr
		}
		headersStr = x
		headersPtr = &headersStr
	}

	if headersPtr != nil && phpMailDetectMultipleCRLF(*headersPtr) {
		ctx.VM().Warning("mail(): Multiple or malformed newlines found in additional_header")
		return vm.Bool(false)
	}

	sep := "\r\n"
	if iniEnabled("mail.mixed_lf_and_crlf") {
		sep = "\n"
	}

	sendmailPath, ok := iniValues["sendmail_path"]
	if !ok {
		sendmailPath = "/usr/sbin/sendmail -t -i"
	}
	if sendmailPath == "" {
		return vm.Bool(false)
	}
	forceExtra, _ := iniValues["mail.force_extra_parameters"]
	cmd := sendmailPath
	if forceExtra != "" {
		cmd = cmd + " " + phpEscapeShellCmd(forceExtra)
	} else if extraCmd != "" {
		cmd = cmd + " " + phpEscapeShellCmd(extraCmd)
	}

	var b strings.Builder
	b.WriteString("To: " + to + sep)
	b.WriteString("Subject: " + subject + sep)
	if headersPtr != nil {
		b.WriteString(*headersPtr + sep)
	}
	b.WriteString(sep + message + sep)

	c := exec.Command("/bin/sh", "-c", cmd)
	c.Stdin = strings.NewReader(b.String())
	var stderr strings.Builder
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// phpMailBuildHeaders повторяет php_mail_build_headers: собирает заголовки из
// массива, проверяя имена и значения (RFC-ограничения Zend).
func phpMailBuildHeaders(ctx *vm.Context, arr *vm.Array) (string, bool) {
	var b strings.Builder
	fail := func(class, msg string) (string, bool) {
		ctx.Throw(vm.NewThrowable(class, msg))
		return "", false
	}
	for k, ev := range arr.All() {
		if !k.IsStr() {
			return fail("TypeError", fmt.Sprintf("Header name cannot be numeric, %d given", k.IntVal()))
		}
		key := k.StrVal()
		v := derefSlot(ev)
		lk := strings.ToLower(key)
		known := false
		for _, n := range []string{"orig-date", "from", "sender", "reply-to", "cc", "bcc", "message-id", "references", "in-reply-to"} {
			if lk == n {
				known = true
				break
			}
		}
		if lk == "to" {
			return fail("ValueError", `The additional headers cannot contain the "To" header`)
		}
		if lk == "subject" {
			return fail("ValueError", `The additional headers cannot contain the "Subject" header`)
		}
		switch v.Type() {
		case vm.StringType:
			s, ok := phpMailHeaderElem(ctx, key, v.StringVal())
			if !ok {
				return "", false
			}
			b.WriteString(s)
		case vm.ArrayType:
			if known {
				return fail("TypeError", fmt.Sprintf("Header \"%s\" must be of type string, array given", lk))
			}
			s, ok := phpMailHeaderElems(ctx, key, v.AsArray())
			if !ok {
				return "", false
			}
			b.WriteString(s)
		default:
			return fail("TypeError", fmt.Sprintf("Header \"%s\" must be of type array|string, %s given", cstrDisplay(key), reflectTypeName(v)))
		}
	}
	s := b.String()
	if len(s) >= 2 {
		s = s[:len(s)-2] // убрать последний CRLF
	}
	return s, true
}

// phpMailHeaderElems — значения-массивы заголовка: только числовые ключи и
// строковые значения.
func phpMailHeaderElems(ctx *vm.Context, key string, arr *vm.Array) (string, bool) {
	var b strings.Builder
	for k, ev := range arr.All() {
		if k.IsStr() {
			ctx.Throw(vm.NewThrowable("TypeError",
				fmt.Sprintf("Header \"%s\" must only contain numeric keys, \"%s\" found", cstrDisplay(key), cstrDisplay(k.StrVal()))))
			return "", false
		}
		v := derefSlot(ev)
		if v.Type() != vm.StringType {
			ctx.Throw(vm.NewThrowable("TypeError",
				fmt.Sprintf("Header \"%s\" must only contain values of type string, %s found", cstrDisplay(key), reflectTypeName(v))))
			return "", false
		}
		s, ok := phpMailHeaderElem(ctx, key, v.StringVal())
		if !ok {
			return "", false
		}
		b.WriteString(s)
	}
	return b.String(), true
}

// phpMailHeaderElem проверяет имя и значение одного заголовка.
func phpMailHeaderElem(ctx *vm.Context, key, val string) (string, bool) {
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < 33 || c > 126 || c == ':' {
			ctx.Throw(vm.NewThrowable("ValueError",
				fmt.Sprintf("Header name \"%s\" contains invalid characters", cstrDisplay(key))))
			return "", false
		}
	}
	switch phpMailCheckFieldValue(val) {
	case mailHdrOK:
	case mailHdrLFOnly:
		ctx.Throw(vm.NewThrowable("ValueError",
			fmt.Sprintf("Header \"%s\" contains LF character that is not allowed in the header", cstrDisplay(key))))
		return "", false
	case mailHdrCROnly:
		ctx.Throw(vm.NewThrowable("ValueError",
			fmt.Sprintf("Header \"%s\" contains CR character that is not allowed in the header", cstrDisplay(key))))
		return "", false
	case mailHdrCRLF:
		ctx.Throw(vm.NewThrowable("ValueError",
			fmt.Sprintf("Header \"%s\" contains CRLF characters that are used as a line separator and are not allowed in the header", cstrDisplay(key))))
		return "", false
	case mailHdrNull:
		ctx.Throw(vm.NewThrowable("ValueError",
			fmt.Sprintf("Header \"%s\" contains NULL character that is not allowed in the header", cstrDisplay(key))))
		return "", false
	}
	return key + ": " + val + "\r\n", true
}

type mailHdrErr int

const (
	mailHdrOK mailHdrErr = iota
	mailHdrCROnly
	mailHdrCRLF
	mailHdrLFOnly
	mailHdrNull
)

// phpMailCheckFieldValue — php_mail_build_headers_check_field_value.
func phpMailCheckFieldValue(v string) mailHdrErr {
	i := 0
	for i < len(v) {
		if v[i] == '\r' {
			if i+1 >= len(v) || v[i+1] != '\n' {
				return mailHdrCROnly
			}
			if len(v)-i >= 3 && (v[i+2] == ' ' || v[i+2] == '\t') {
				i += 3
				continue
			}
			return mailHdrCRLF
		}
		if v[i] == '\n' {
			if len(v)-i >= 2 && (v[i+1] == ' ' || v[i+1] == '\t') {
				i += 2
				continue
			}
			return mailHdrLFOnly
		}
		if v[i] == 0 {
			return mailHdrNull
		}
		i++
	}
	return mailHdrOK
}

// phpMailDetectMultipleCRLF — обнаружение множественных/неверных переводов
// строк в дополнительных заголовках.
func phpMailDetectMultipleCRLF(hdr string) bool {
	if hdr == "" {
		return false
	}
	if hdr[0] < 33 || hdr[0] > 126 || hdr[0] == ':' {
		return true
	}
	for i := 0; i < len(hdr); {
		if hdr[i] == '\r' {
			if i+1 >= len(hdr) || hdr[i+1] == '\r' || (hdr[i+1] == '\n' && (i+2 >= len(hdr) || hdr[i+2] == '\n' || hdr[i+2] == '\r')) {
				return true
			}
			i += 2
		} else if hdr[i] == '\n' {
			if i+1 >= len(hdr) || hdr[i+1] == '\r' || hdr[i+1] == '\n' {
				return true
			}
			i += 2
		} else {
			i++
		}
	}
	return false
}

// mailSanitizeHeader повторяет обработку $to/$subject в mail(): правый trim
// пробелов и замена управляющих символов пробелом (кроме сложенных CRLF+WSP).
func mailSanitizeHeader(s string) string {
	b := []byte(s)
	end := len(b)
	for end > 0 && isMailSpace(b[end-1]) {
		end--
	}
	b = b[:end]
	for i := 0; i < len(b); {
		if isMailCntrl(b[i]) {
			if b[i] == '\r' && i+1 < len(b) && b[i+1] == '\n' && i+2 < len(b) && (b[i+2] == ' ' || b[i+2] == '\t') {
				i += 2
				for i+1 < len(b) && (b[i+1] == ' ' || b[i+1] == '\t') {
					i++
				}
				i++
				continue
			}
			b[i] = ' '
		}
		i++
	}
	return string(b)
}

func isMailSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isMailCntrl(c byte) bool {
	return c < 32 || c == 127
}

// mailTrimRight удаляет символы из charlist справа.
func mailTrimRight(s, cut string) string {
	return strings.TrimRight(s, cut)
}

// mailBasename — php_basename(path, NULL, 0).
func mailBasename(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// phpEscapeShellCmd повторяет php_escape_shell_cmd: экранирует метасимволы.
func phpEscapeShellCmd(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '?', '[', ']', '/', '<', '>', ':', ';', '|', '\'', '"', ' ', '\t', '\n', '\r', '\v', '\f', '$', '&', '`', '(', ')', '{', '}', '#', '!', '~', '*', ',':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

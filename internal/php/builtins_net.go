package php

import (
	"net"
	"os"
	"sort"
	"strconv"
	"strings"

	"phpvm/internal/vm"
)

// Файл содержит сетевые функции (dns/ip/cookie/syslog), доступные в CLI.

// nullByteCheck поднимает ValueError, если строка содержит NUL (PHP 8:
// "X(): Argument #N ($param) must not contain any null bytes").
func nullByteCheck(ctx *vm.Context, s, fn string, argNum int, param string) bool {
	if strings.IndexByte(s, 0) >= 0 {
		ctx.Throw(vm.NewThrowable("ValueError", fn+"(): Argument #"+itoa(argNum)+
			" ($"+param+") must not contain any null bytes"))
		return false
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ip2long — ip2long(): IPv4-строка -> целое (unsigned 32-bit), иначе false.
func ip2longFn(ctx *vm.Context) vm.Value {
	s, ok := strParam(ctx, 0, "ip2long", "ip")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, s, "ip2long", 1, "ip") {
		return *new(vm.Value)
	}
	v := parseIPv4Uint32(s)
	if v < 0 {
		return vm.Bool(false)
	}
	return vm.Int(v)
}

func parseIPv4Uint32(s string) int64 {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return -1
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return -1
	}
	return int64(uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3]))
}

// long2ip — long2ip(): целое -> IPv4-строка.
func long2ipFn(ctx *vm.Context) vm.Value {
	i, ok := numArg(ctx, 0, "long2ip", "ip")
	if !ok {
		return *new(vm.Value)
	}
	v := uint32(i.ToInt())
	return vm.Str(itoa(int(v>>24)) + "." + itoa(int((v>>16)&0xFF)) + "." +
		itoa(int((v>>8)&0xFF)) + "." + itoa(int(v&0xFF)))
}

// firstIPv4 возвращает первый IPv4-адрес хоста.
func firstIPv4(host string) (string, bool) {
	addrs, err := net.LookupHost(host)
	if err != nil {
		return "", false
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return ip.To4().String(), true
		}
	}
	if len(addrs) > 0 {
		return addrs[0], true
	}
	return "", false
}

// gethostbyname — имя хоста -> IPv4 (или исходная строка при неудаче).
func gethostbynameFn(ctx *vm.Context) vm.Value {
	s, ok := strParam(ctx, 0, "gethostbyname", "hostname")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, s, "gethostbyname", 1, "hostname") {
		return *new(vm.Value)
	}
	if len(s) > 255 {
		ctx.VM().Warn("gethostbyname(): Host name cannot be longer than 255 characters")
		return vm.Str(s)
	}
	if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
		return vm.Str(ip.To4().String())
	}
	if ip, ok := firstIPv4(s); ok {
		return vm.Str(ip)
	}
	return vm.Str(s)
}

// gethostbynamel — имя хоста -> массив IPv4-адресов (false при неудаче).
func gethostbynamelFn(ctx *vm.Context) vm.Value {
	s, ok := strParam(ctx, 0, "gethostbynamel", "hostname")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, s, "gethostbynamel", 1, "hostname") {
		return *new(vm.Value)
	}
	if len(s) > 255 {
		ctx.VM().Warn("gethostbynamel(): Host name cannot be longer than 255 characters")
		return vm.Bool(false)
	}
	addrs, err := net.LookupHost(s)
	if err != nil {
		return vm.Bool(false)
	}
	out := vm.NewArray()
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			out.Append(vm.MakeString(a))
		}
	}
	if out.Len() == 0 {
		return vm.Bool(false)
	}
	return vm.MakeArray(out)
}

// gethostbyaddr — IP -> имя хоста (false при неудаче).
func gethostbyaddrFn(ctx *vm.Context) vm.Value {
	s, ok := strParam(ctx, 0, "gethostbyaddr", "ip")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, s, "gethostbyaddr", 1, "ip") {
		return *new(vm.Value)
	}
	names, err := net.LookupAddr(s)
	if err != nil || len(names) == 0 {
		return vm.Bool(false)
	}
	return vm.Str(strings.TrimSuffix(names[0], "."))
}

// validDNSCheckTypes — допустимые значения $type у checkdnsrr/dns_check_record.
var validDNSCheckTypes = map[string]bool{
	"A": true, "MX": true, "NS": true, "SOA": true, "PTR": true,
	"CNAME": true, "AAAA": true, "A6": true, "SRV": true, "NAPTR": true,
	"TXT": true, "ANY": true,
}

// dnsCheckRecordFn — checkdnsrr()/dns_check_record(): есть ли запись типа type.
func dnsCheckRecordFn(ctx *vm.Context) vm.Value {
	host, ok := strParam(ctx, 0, "dns_check_record", "hostname")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, host, "dns_check_record", 1, "hostname") {
		return *new(vm.Value)
	}
	if host == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "dns_check_record(): Argument #1 ($hostname) cannot be empty"))
		return *new(vm.Value)
	}
	typ := "MX"
	if len(ctx.Args()) >= 2 {
		t, ok := strParam(ctx, 1, "dns_check_record", "type")
		if !ok {
			return *new(vm.Value)
		}
		typ = strings.ToUpper(t)
	}
	if !validDNSCheckTypes[typ] {
		ctx.Throw(vm.NewThrowable("ValueError", "dns_check_record(): Argument #2 ($type) must be a valid DNS record type"))
		return *new(vm.Value)
	}
	return vm.Bool(dnsLookupExists(host, typ))
}

func dnsLookupExists(host, typ string) bool {
	switch typ {
	case "A", "ANY":
		if _, err := net.LookupHost(host); err == nil {
			return true
		}
		if typ == "A" {
			return false
		}
		fallthrough
	case "MX":
		if mx, err := net.LookupMX(host); err == nil && len(mx) > 0 {
			return true
		}
	case "CNAME":
		if c, err := net.LookupCNAME(host); err == nil && c != "" {
			return true
		}
	case "NS":
		if ns, err := net.LookupNS(host); err == nil && len(ns) > 0 {
			return true
		}
	case "TXT":
		if t, err := net.LookupTXT(host); err == nil && len(t) > 0 {
			return true
		}
	case "PTR":
		if n, err := net.LookupAddr(host); err == nil && len(n) > 0 {
			return true
		}
	}
	return false
}

// Константы типов dns_get_record (ext/standard/dns.c).
const (
	dnsA     = 1
	dnsNS    = 2
	dnsCNAME = 16
	dnsSOA   = 32
	dnsPTR   = 2048
	dnsHINFO = 4096
	dnsMX    = 16384
	dnsTXT   = 32768
	dnsSRV   = 33554432
	dnsNAPTR = 134217728
	dnsAAAA  = 268435456
	dnsA6    = 16777216
	dnsANY   = 268435456
	dnsALL   = 251721779
)

func validDNSType(t int64) bool {
	switch t {
	case dnsA, dnsNS, dnsCNAME, dnsSOA, dnsPTR, dnsHINFO, dnsMX, dnsTXT,
		dnsSRV, dnsNAPTR, dnsAAAA, dnsA6, dnsALL:
		return true
	}
	return false
}

// dnsGetRecordFn — dns_get_record(): записи DNS (базовая поддержка A/AAAA/MX/TXT/NS/CNAME).
func dnsGetRecordFn(ctx *vm.Context) vm.Value {
	host, ok := strParam(ctx, 0, "dns_get_record", "hostname")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, host, "dns_get_record", 1, "hostname") {
		return *new(vm.Value)
	}
	typ := int64(dnsANY)
	if len(ctx.Args()) >= 2 {
		v, ok := numArg(ctx, 1, "dns_get_record", "type")
		if !ok {
			return *new(vm.Value)
		}
		typ = v.ToInt()
	}
	raw := false
	if len(ctx.Args()) >= 5 {
		raw = derefSlot(arg(ctx, 4)).ToBool()
	}
	if raw {
		if typ < 1 || typ > 65535 {
			ctx.Throw(vm.NewThrowable("ValueError",
				"dns_get_record(): Argument #2 ($type) must be between 1 and 65535 when argument #5 ($raw) is true"))
			return *new(vm.Value)
		}
	} else if !validDNSType(typ) {
		ctx.Throw(vm.NewThrowable("ValueError", "dns_get_record(): Argument #2 ($type) must be a DNS_* constant"))
		return *new(vm.Value)
	}
	out := vm.NewArray()
	add := func(rec map[string]vm.Slot) {
		a := vm.NewArray()
		keys := make([]string, 0, len(rec))
		for k := range rec {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			a.Set(vm.MakeStrKey(k), rec[k])
		}
		out.Append(vm.MakeArray(a))
	}
	if typ&(dnsA|dnsANY|dnsALL) != 0 {
		if ips, err := net.LookupHost(host); err == nil {
			for _, ipStr := range ips {
				ip := net.ParseIP(ipStr)
				if ip == nil || ip.To4() == nil {
					continue
				}
				add(map[string]vm.Slot{"host": vm.MakeString(host), "type": vm.MakeString("A"), "ip": vm.MakeString(ip.To4().String())})
			}
		}
	}
	if typ&(dnsMX|dnsANY|dnsALL) != 0 {
		if mxs, err := net.LookupMX(host); err == nil {
			for _, mx := range mxs {
				add(map[string]vm.Slot{"host": vm.MakeString(host), "type": vm.MakeString("MX"),
					"pri": vm.MakeInt(int64(mx.Pref)), "target": vm.MakeString(strings.TrimSuffix(mx.Host, "."))})
			}
		}
	}
	if typ&(dnsTXT|dnsANY|dnsALL) != 0 {
		if txts, err := net.LookupTXT(host); err == nil {
			for _, t := range txts {
				add(map[string]vm.Slot{"host": vm.MakeString(host), "type": vm.MakeString("TXT"), "txt": vm.MakeString(t)})
			}
		}
	}
	if typ&(dnsNS|dnsANY|dnsALL) != 0 {
		if nss, err := net.LookupNS(host); err == nil {
			for _, ns := range nss {
				add(map[string]vm.Slot{"host": vm.MakeString(host), "type": vm.MakeString("NS"),
					"target": vm.MakeString(strings.TrimSuffix(ns.Host, "."))})
			}
		}
	}
	if typ&(dnsCNAME|dnsANY|dnsALL) != 0 {
		if c, err := net.LookupCNAME(host); err == nil && strings.TrimSuffix(c, ".") != host {
			add(map[string]vm.Slot{"host": vm.MakeString(host), "type": vm.MakeString("CNAME"), "target": vm.MakeString(strings.TrimSuffix(c, "."))})
		}
	}
	return vm.MakeArray(out)
}

// dnsGetMxFn — dns_get_mx()/getmxrr(): MX-записи хоста.
func dnsGetMxFn(ctx *vm.Context) vm.Value {
	host, ok := strParam(ctx, 0, "dns_get_mx", "hostname")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, host, "dns_get_mx", 1, "hostname") {
		return *new(vm.Value)
	}
	mxs, err := net.LookupMX(host)
	hosts := vm.NewArray()
	weights := vm.NewArray()
	if err == nil {
		for _, mx := range mxs {
			hosts.Append(vm.MakeString(strings.TrimSuffix(mx.Host, ".")))
			weights.Append(vm.MakeInt(int64(mx.Pref)))
		}
	}
	netWriteRef(ctx, 1, vm.MakeArray(hosts))
	netWriteRef(ctx, 2, vm.MakeArray(weights))
	if err != nil || hosts.Len() == 0 {
		return vm.Bool(false)
	}
	return vm.Bool(true)
}

// socketGetStatusFn — socket_get_status(): алиас stream_get_meta_data().
func socketGetStatusFn(ctx *vm.Context) vm.Value {
	return streamGetMetaDataFn(ctx)
}

// closelog/openlog/syslog — заглушки CLI (журналирование не поддерживается).
func closelogFn(ctx *vm.Context) vm.Value { return vm.Bool(true) }
func openlogFn(ctx *vm.Context) vm.Value  { return vm.Bool(true) }
func syslogFn(ctx *vm.Context) vm.Value   { return vm.Bool(true) }

// netWriteRef записывает значение в by-ref аргумент #i (если он есть).
func netWriteRef(ctx *vm.Context, i int, v vm.Value) {
	args := ctx.Args()
	if i < 0 || i >= len(args) {
		return
	}
	s := args[i]
	if s.IsRef() && s.Ref() != nil {
		var out vm.Slot
		vm.SlotOf(&out, v)
		s.Ref().Set(out)
	}
}

// readEtcFile читает /etc/protocols или /etc/services, пропуская комментарии.
func etcLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(ln, '#'); i >= 0 {
			ln = ln[:i]
		}
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, ln)
	}
	return out
}

func getprotobynameFn(ctx *vm.Context) vm.Value {
	name, ok := strParam(ctx, 0, "getprotobyname", "protocol")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, name, "getprotobyname", 1, "protocol") {
		return *new(vm.Value)
	}
	for _, ln := range etcLines("/etc/protocols") {
		f := strings.Fields(ln)
		if len(f) >= 2 && strings.EqualFold(f[0], name) {
			if n, err := strconv.Atoi(f[1]); err == nil {
				return vm.Int(int64(n))
			}
		}
	}
	return vm.Bool(false)
}

func getservbynameFn(ctx *vm.Context) vm.Value {
	service, ok := strParam(ctx, 0, "getservbyname", "service")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, service, "getservbyname", 1, "service") {
		return *new(vm.Value)
	}
	proto := ""
	if len(ctx.Args()) >= 2 {
		pr, ok := strParam(ctx, 1, "getservbyname", "protocol")
		if !ok {
			return *new(vm.Value)
		}
		if !nullByteCheck(ctx, pr, "getservbyname", 2, "protocol") {
			return *new(vm.Value)
		}
		proto = pr
	}
	for _, ln := range etcLines("/etc/services") {
		f := strings.Fields(ln)
		if len(f) < 2 || !strings.EqualFold(f[0], service) {
			continue
		}
		pp := strings.SplitN(f[1], "/", 2)
		if len(pp) != 2 {
			continue
		}
		if proto != "" && !strings.EqualFold(pp[1], proto) {
			continue
		}
		if n, err := strconv.Atoi(pp[0]); err == nil {
			return vm.Int(int64(n))
		}
	}
	return vm.Bool(false)
}

func getservbyportFn(ctx *vm.Context) vm.Value {
	port, ok := numArg(ctx, 0, "getservbyport", "port")
	if !ok {
		return *new(vm.Value)
	}
	proto := ""
	if len(ctx.Args()) >= 2 {
		pr, ok := strParam(ctx, 1, "getservbyport", "protocol")
		if !ok {
			return *new(vm.Value)
		}
		if !nullByteCheck(ctx, pr, "getservbyport", 2, "protocol") {
			return *new(vm.Value)
		}
		proto = pr
	}
	want := int(port.ToInt())
	for _, ln := range etcLines("/etc/services") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		pp := strings.SplitN(f[1], "/", 2)
		if len(pp) != 2 {
			continue
		}
		if proto != "" && !strings.EqualFold(pp[1], proto) {
			continue
		}
		if n, err := strconv.Atoi(pp[0]); err == nil && n == want {
			return vm.Str(f[0])
		}
	}
	return vm.Bool(false)
}

func inetPtonFn(ctx *vm.Context) vm.Value {
	s, ok := strParam(ctx, 0, "inet_pton", "ip")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, s, "inet_pton", 1, "ip") {
		return *new(vm.Value)
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return vm.Bool(false)
	}
	if ip4 := ip.To4(); ip4 != nil {
		return vm.MakeString(string(ip4))
	}
	return vm.MakeString(string(ip.To16()))
}

func inetNtopFn(ctx *vm.Context) vm.Value {
	s, ok := strParam(ctx, 0, "inet_ntop", "ip")
	if !ok {
		return *new(vm.Value)
	}
	if !nullByteCheck(ctx, s, "inet_ntop", 1, "ip") {
		return *new(vm.Value)
	}
	switch len(s) {
	case 4, 16:
		ip := net.IP([]byte(s))
		if ip == nil {
			return vm.Bool(false)
		}
		return vm.Str(ip.String())
	}
	return vm.Bool(false)
}

// fccFn — __php_fcc(kind, a, b): first-class callable (PHP 8.1).
// kind 1 — функция по индексу, 2 — метод объекта, 3 — статический метод
// (имя класса), 4 — функция по имени.
func fccFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) == 0 {
		return vm.Bool(false)
	}
	kind := int(derefSlot(a[0]).ToInt())
	switch kind {
	case 1:
		if len(a) < 2 {
			return vm.Bool(false)
		}
		fn := ctx.VM().FuncAt(int(derefSlot(a[1]).ToInt()))
		if fn == nil {
			return vm.Bool(false)
		}
		return vm.MakeClosure(&vm.Closure{Fn: fn, Fake: true})
	case 2:
		if len(a) < 3 {
			return vm.Bool(false)
		}
		recv := derefSlot(a[1])
		ci := ctx.VM().SlotClassInfo(recv)
		if ci == nil {
			return vm.Bool(false)
		}
		res2, err := ctx.VM().FCCObjectSlot(ci, recv, derefSlot(a[2]).ToString())
		if err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
			} else {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
			}
			return *new(vm.Value)
		}
		return res2
	case 3:
		if len(a) < 3 {
			return vm.Bool(false)
		}
		res3, err := ctx.VM().FCCStaticSlot(derefSlot(a[1]), derefSlot(a[2]).ToString())
		if err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
			} else {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
			}
			return *new(vm.Value)
		}
		return res3
	case 5:
		if len(a) < 2 {
			return vm.Bool(false)
		}
		res5, err := ctx.VM().FCCValue(a[1])
		if err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
			} else {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
			}
			return *new(vm.Value)
		}
		return res5
	case 4:
		if len(a) < 2 {
			return vm.Bool(false)
		}
		cl, err := ctx.VM().ClosureFromSlot(vm.MakeString(derefSlot(a[1]).ToString()))
		if err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
			}
			return *new(vm.Value)
		}
		return cl
	}
	return vm.Bool(false)
}

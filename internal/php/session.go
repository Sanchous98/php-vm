package php

// session.go — минимальная поддержка ext/session: session_start(),
// session_decode() с обработчиками "php" и "php_serialize" (семантика Zend,
// включая разделяемый var_hash вложенного unserialize и предупреждения при
// сбое разбора). Файловое хранилище не эмулируется: сессия пустая.

import (
	"strings"

	"phpvm/internal/vm"
)

// sessionHandlerName возвращает текущий session.serialize_handler.
func sessionHandlerName() string {
	if v, ok := IniValue("session.serialize_handler"); ok && v != "" {
		return strings.ToLower(v)
	}
	return "php"
}

// sessionDecodeFn — session_decode($data).
func sessionDecodeFn(ctx *vm.Context) vm.Value {
	vmc := ctx.VM()
	if !vmc.SessionActive() {
		vmc.Warn("session_decode(): Session data cannot be decoded when there is no active session")
		return vm.Bool(false)
	}
	data := arg0(ctx).ToString()
	// Общее состояние разбора: активный var_hash внешнего unserialize()
	// переиспользуется, пока не выставлен serialize_lock (Zend
	// php_var_unserialize_init: BG(unserialize).data).
	shared := (*unserShared)(nil)
	restore := func() {}
	var u *phpUnser
	if cur, ok := vmc.UnserState().(*phpUnser); ok && cur != nil && !vmc.SerializeLocked() {
		shared = cur.shared
		u = &phpUnser{vm: vmc, ctx: ctx, s: data, shared: shared, warnPrefix: "session_decode():"}
	} else {
		shared = &unserShared{}
		u = &phpUnser{vm: vmc, ctx: ctx, s: data, shared: shared, warnPrefix: "session_decode():"}
		prev := vmc.UnserState()
		vmc.SetUnserState(u)
		restore = func() { vmc.SetUnserState(prev) }
	}
	defer restore()
	startVals := len(shared.vals)
	// fail: Zend php_session_decode → php_session_cancel_decode: сессия
	// уничтожается и создаётся заново (пустой $_SESSION), плюс предупреждение.
	fail := func() vm.Value {
		// Неуспешный разбор откатывает добавленные им значения var_hash, чтобы
		// ссылки R:/r: на частично разобранные значения стали недействительными
		// (Zend: mark all elements as NULL).
		for i := startVals; i < len(shared.vals); i++ {
			shared.vals[i].invalid = true
			vmc.MarkNoDestruct(shared.vals[i].obj)
		}
		vmc.Warn("session_decode(): Failed to decode session object. Session has been destroyed")
		vmc.SetSuperglobalSynced("$_SESSION", vm.MakeArray(vm.NewArray()))
		return vm.Bool(false)
	}
	switch sessionHandlerName() {
	case "php_serialize":
		val, np, ok := u.value(0)
		if !ok || np < len(data) {
			return fail()
		}
		vmc.SetSuperglobalSynced("$_SESSION", val)
		return vm.Bool(true)
	case "php":
		arr := vm.NewArray()
		p := 0
		for p < len(data) {
			bar := strings.IndexByte(data[p:], '|')
			if bar < 0 {
				return fail()
			}
			name := data[p : p+bar]
			p += bar + 1
			if p >= len(data) {
				return fail()
			}
			val, np, ok := u.value(p)
			if !ok {
				return fail()
			}
			arr.SetStr(name, val)
			p = np
		}
		vmc.SetSuperglobalSynced("$_SESSION", vm.MakeArray(arr))
		return vm.Bool(true)
	}
	vmc.Warn("session_decode(): Unknown session.serialize_handler. Failed to decode session object")
	return vm.Bool(false)
}

// sessionStartFn — session_start(): активная сессия с пустым $_SESSION
// (файловое хранилище не эмулируется).
func sessionStartFn(ctx *vm.Context) vm.Value {
	vmc := ctx.VM()
	vmc.SetSessionActive(true)
	vmc.SetSuperglobalSynced("$_SESSION", vm.MakeArray(vm.NewArray()))
	// trans-sid: SID добавляется сканером (session-канал) только при
	// use_trans_sid && !use_only_cookies.
	if iniBoolInt(iniValues["session.use_trans_sid"]) != 0 &&
		iniBoolInt(iniValues["session.use_only_cookies"]) == 0 {
		urlRewriterActivate(ctx, true)
		name := iniValues["session.name"]
		if name == "" {
			name = "PHPSESSID"
		}
		id := sessionIDValue
		if id == "" {
			id = phpSessionGenerateID()
		}
		urlRewriteAddVar(&urlRewriteSess, name, id)
	}
	return vm.Bool(true)
}

// sessionDestroyFn — session_destroy(): снимает активность, $_SESSION пуст.
func sessionDestroyFn(ctx *vm.Context) vm.Value {
	vmc := ctx.VM()
	if !vmc.SessionActive() {
		vmc.Warn("session_destroy(): Trying to destroy uninitialized session")
		return vm.Bool(false)
	}
	vmc.SetSessionActive(false)
	vmc.SetSuperglobalSynced("$_SESSION", vm.MakeArray(vm.NewArray()))
	return vm.Bool(true)
}

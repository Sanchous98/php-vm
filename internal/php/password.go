package php

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"phpvm/internal/vm"
)

// Файл содержит password_hash/password_verify/password_get_info/
// password_needs_rehash/password_algos (порт ext/standard/password.c, bcrypt).

// passwordBcryptAlgo — идентификатор bcrypt в PHP 8.3 (PASSWORD_BCRYPT).
const passwordBcryptAlgo = "2y"

const passwordBcryptDefaultCost = 10

var passwordAlgorithms = []string{"2y", "argon2i", "argon2id"}

// passwordAlgoFromArg приводит аргумент $algo (string|int|null) к строке
// алгоритма. ok=false — исключение уже поднято.
func passwordAlgoFromArg(ctx *vm.Context, fn string, s vm.Slot) (string, bool) {
	s = derefSlot(s)
	switch s.Type() {
	case vm.NullType:
		return passwordBcryptAlgo, true
	case vm.StringType:
		name := s.StringVal()
		for _, a := range passwordAlgorithms {
			if a == name {
				return name, true
			}
		}
		ctx.Throw(vm.NewThrowable("ValueError",
			fn+": Argument #2 ($algo) must be a valid password hashing algorithm"))
		return "", false
	case vm.IntType:
		// Устаревшие целочисленные идентификаторы (PHP < 8.3).
		switch s.ToInt() {
		case 0, 1:
			return passwordBcryptAlgo, true
		}
		return passwordBcryptAlgo, true
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		vm.ArgTypeError(fn, 2, "algo", "string|int|null", phpTypeName(ctx, 1), "", 0)))
	return "", false
}

// bcryptSalt генерирует соль вида "$2y$<cost>$<22 символа bcrypt-base64>".
func bcryptSalt(cost int64) string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	enc := base64.NewEncoding("./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789").WithPadding(base64.NoPadding)
	return fmt.Sprintf("$2y$%02d$%s", cost, enc.EncodeToString(raw))
}

// passwordBcryptInfo распознаёт bcrypt-хэш и возвращает cost.
func passwordBcryptInfo(hash string) (bool, int64) {
	if len(hash) != 60 || !strings.HasPrefix(hash, "$2") {
		return false, 0
	}
	if hash[3] != '$' {
		return false, 0
	}
	cost := int64(0)
	for i := 4; i < 6; i++ {
		c := hash[i]
		if c < '0' || c > '9' {
			return false, 0
		}
		cost = cost*10 + int64(c-'0')
	}
	if hash[6] != '$' {
		return false, 0
	}
	return true, cost
}

func passwordHashFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "password_hash() expects at least 2 arguments, 1 given"))
		return *new(vm.Value)
	}
	pw, ok := strParam(ctx, 0, "password_hash", "password")
	if !ok {
		return *new(vm.Value)
	}
	algo, ok := passwordAlgoFromArg(ctx, "password_hash", a[1])
	if !ok {
		return *new(vm.Value)
	}
	cost := int64(passwordBcryptDefaultCost)
	if len(a) >= 3 && derefSlot(a[2]).Type() != vm.NullType {
		opts := derefSlot(a[2])
		if opts.Type() != vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("password_hash", 3, "options", "array", phpTypeName(ctx, 2), "", 0)))
			return *new(vm.Value)
		}
		arr := opts.AsArray()
		if v, found := arr.Get(vm.MakeStrKey("cost")); found {
			cost = v.ToInt()
		}
		if _, found := arr.Get(vm.MakeStrKey("salt")); found {
			ctx.VM().Warning(`password_hash(): The "salt" option has been ignored, since providing a custom salt is no longer supported`)
		}
	}
	if algo != passwordBcryptAlgo {
		ctx.Throw(vm.NewThrowable("ValueError",
			"password_hash(): Argument #2 ($algo) must be a valid password hashing algorithm"))
		return *new(vm.Value)
	}
	if cost < 4 || cost > 31 {
		ctx.Throw(vm.NewThrowable("ValueError", fmt.Sprintf("Invalid bcrypt cost parameter specified: %d", cost)))
		return *new(vm.Value)
	}
	if strings.IndexByte(pw, 0) >= 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "Bcrypt password must not contain null character"))
		return *new(vm.Value)
	}
	hash := phpCrypt(pw, bcryptSalt(cost))
	if hash == "" || strings.HasPrefix(hash, "*") {
		return vm.Bool(false)
	}
	return vm.Str(hash)
}

func passwordVerifyFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) != 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("password_verify() expects exactly 2 arguments, %d given", len(a))))
		return *new(vm.Value)
	}
	pw := derefSlot(a[0]).ToString()
	hs := derefSlot(a[1])
	if hs.Type() == vm.NullType {
		ctx.VM().Deprecated("password_verify(): Passing null to parameter #2 ($hash) of type string is deprecated")
		return vm.Bool(false)
	}
	hash := hs.ToString()
	if len(hash) < 7 {
		return vm.Bool(false)
	}
	computed := phpCrypt(pw, hash)
	return vm.Bool(computed == hash)
}

func passwordGetInfoFn(ctx *vm.Context) vm.Value {
	hash, ok := strParam(ctx, 0, "password_get_info", "hash")
	if !ok {
		return *new(vm.Value)
	}
	algo, name, cost, known := passwordInfo(hash)
	out := vm.NewArray()
	if known {
		out.Set(vm.MakeStrKey("algo"), vm.MakeString(algo))
		out.Set(vm.MakeStrKey("algoName"), vm.MakeString(name))
		opts := vm.NewArray()
		opts.Set(vm.MakeStrKey("cost"), vm.MakeInt(cost))
		out.Set(vm.MakeStrKey("options"), vm.MakeArray(opts))
	} else {
		out.Set(vm.MakeStrKey("algo"), vm.MakeNull())
		out.Set(vm.MakeStrKey("algoName"), vm.MakeString("unknown"))
		out.Set(vm.MakeStrKey("options"), vm.MakeArray(vm.NewArray()))
	}
	return vm.MakeArray(out)
}

// passwordInfo распознаёт алгоритм хэша: bcrypt ($2y$/$2a$/$2b$ длиной 60).
func passwordInfo(hash string) (algo, name string, cost int64, ok bool) {
	if isBcrypt, c := passwordBcryptInfo(hash); isBcrypt {
		return passwordBcryptAlgo, "bcrypt", c, true
	}
	return "", "", 0, false
}

func passwordNeedsRehashFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "password_needs_rehash() expects at least 2 arguments, 1 given"))
		return *new(vm.Value)
	}
	hash, ok := strParam(ctx, 0, "password_needs_rehash", "hash")
	if !ok {
		return *new(vm.Value)
	}
	algo, ok := passwordAlgoFromArg(ctx, "password_needs_rehash", a[1])
	if !ok {
		return *new(vm.Value)
	}
	if len(a) >= 3 && derefSlot(a[2]).Type() != vm.NullType && derefSlot(a[2]).Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("password_needs_rehash", 3, "options", "array", phpTypeName(ctx, 2), "", 0)))
		return *new(vm.Value)
	}
	curAlgo, _, curCost, known := passwordInfo(hash)
	if !known || curAlgo != algo {
		return vm.Bool(true)
	}
	wantCost := int64(passwordBcryptDefaultCost)
	if len(a) >= 3 && derefSlot(a[2]).Type() == vm.ArrayType {
		if v, found := derefSlot(a[2]).AsArray().Get(vm.MakeStrKey("cost")); found {
			wantCost = v.ToInt()
		}
	}
	return vm.Bool(wantCost != curCost)
}

func passwordAlgosFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, a := range passwordAlgorithms {
		out.Append(vm.MakeString(a))
	}
	return vm.MakeArray(out)
}

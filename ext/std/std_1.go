package std

import (
	"php-vm/internal/vm"
	"php-vm/pkg/stdlib"
	"strings"
)

func strtoupper(ctx *vm.FunctionContext) vm.String {
	var str string
	vm.ParseParameters(ctx, &str)
	return vm.String(stdlib.StrToUpper(str))
}

func strtolower(ctx *vm.FunctionContext) vm.String {
	var str string
	vm.ParseParameters(ctx, &str)
	return vm.String(stdlib.StrToLower(str))
}

func strpos(ctx *vm.FunctionContext) vm.Value {
	var str, substr string
	vm.ParseParameters(ctx, &str, &substr)
	res := vm.Int(stdlib.StrPos(str, substr))

	if res == -1 {
		return vm.Bool(false)
	}

	return res
}

func stripos(ctx *vm.FunctionContext) vm.Value {
	var str, substr string
	vm.ParseParameters(ctx, &str, &substr)
	res := vm.Int(stdlib.StrIPos(str, substr))

	if res == -1 {
		return vm.Bool(false)
	}

	return res
}

func strrpos(ctx *vm.FunctionContext) vm.Value {
	var str, substr string
	vm.ParseParameters(ctx, &str, &substr)
	res := vm.Int(stdlib.StrRPos(str, substr))

	if res == -1 {
		return vm.Bool(false)
	}

	return res
}

func strripos(ctx *vm.FunctionContext) vm.Value {
	var str, substr string
	vm.ParseParameters(ctx, &str, &substr)
	res := vm.Int(stdlib.StrRIPos(str, substr))

	if res == -1 {
		return vm.Bool(false)
	}

	return res
}

func strrev(ctx *vm.FunctionContext) vm.String {
	var str string
	vm.ParseParameters(ctx, &str)
	return vm.String(stdlib.StrRev(str))
}

func nl2br(ctx *vm.FunctionContext) vm.String {
	var str string
	vm.ParseParameters(ctx, &str)
	return vm.String(stdlib.Nl2Br(str))
}

func basename(ctx *vm.FunctionContext) vm.String {
	var str, trimSuffix string
	vm.ParseParameters(ctx, &str, trimSuffix)

	return vm.String(stdlib.Basename(str, trimSuffix))
}

func dirname(ctx *vm.FunctionContext) vm.String {
	var str string
	vm.ParseParameters(ctx, &str)
	return vm.String(stdlib.Dirname(str))
}

const (
	PathinfoDirname = vm.Int(1 << iota)
	PathinfoBasename
	PathinfoExtension
	PathinfoFilename
	PathinfoAll = PathinfoDirname | PathinfoBasename | PathinfoExtension | PathinfoFilename
)

func pathinfo(ctx *vm.FunctionContext) *vm.Array {
	var path string
	var flags vm.Int
	vm.ParseParameters(ctx, &path, &flags)

	res := vm.NewArray(nil)

	if flags&PathinfoDirname > 0 {
		res.OffsetSet(ctx, vm.String("dirname"), vm.String(stdlib.Dirname(path)))
	}

	if flags&PathinfoBasename > 0 {
		res.OffsetSet(ctx, vm.String("basename"), vm.String(stdlib.Basename(path, "")))
	}

	if flags&PathinfoExtension > 0 {
		res.OffsetSet(ctx, vm.String("extension"), vm.String(stdlib.Ext(path)))
	}

	if flags&PathinfoFilename > 0 {
		res.OffsetSet(ctx, vm.String("filename"), vm.String(strings.TrimPrefix(path, stdlib.Dirname(path))))
	}

	return res
}

func stripslashes(ctx *vm.FunctionContext) vm.String {
	var str string
	vm.ParseParameters(ctx, str)
	return vm.String(stdlib.StripSlashes(str))
}

func stripcslashes(ctx *vm.FunctionContext) vm.String {
	var str string
	vm.ParseParameters(ctx, str)
	return vm.String(stdlib.StripCSlashes(str))
}

func strstr(ctx *vm.FunctionContext) vm.Value {
	var haystack, needle string
	var beforeNeedle bool
	vm.ParseParameters(ctx, &haystack, &needle, &beforeNeedle)

	if res, found := stdlib.StrStr(haystack, needle, beforeNeedle); found {
		return vm.String(res)
	}

	return vm.Bool(false)
}

func stristr(ctx *vm.FunctionContext) vm.Value {
	var haystack, needle string
	var beforeNeedle bool
	vm.ParseParameters(ctx, &haystack, &needle, &beforeNeedle)

	if res, found := stdlib.StrIStr(haystack, needle, beforeNeedle); found {
		return vm.String(res)
	}

	return vm.Bool(false)
}

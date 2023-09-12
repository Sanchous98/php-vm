package std

import (
	"php-vm/internal/vm"
	"php-vm/pkg/stdlib"
	"strings"
)

func strtoupper(_ vm.Context, args ...vm.Value) vm.String {
	return vm.String(stdlib.StrToUpper(string(args[0].(vm.String))))
}

func strtolower(_ vm.Context, args ...vm.Value) vm.String {
	return vm.String(stdlib.StrToLower(string(args[0].(vm.String))))
}

func strpos(_ vm.Context, args ...vm.Value) vm.Value {
	res := vm.Int(stdlib.StrPos(string(args[0].(vm.String)), string(args[1].(vm.String))))

	if res == -1 {
		return vm.Bool(false)
	}

	return res
}

func stripos(_ vm.Context, args ...vm.Value) vm.Value {
	res := vm.Int(stdlib.StrIPos(string(args[0].(vm.String)), string(args[1].(vm.String))))

	if res == -1 {
		return vm.Bool(false)
	}

	return res
}

func strrpos(_ vm.Context, args ...vm.Value) vm.Value {
	res := vm.Int(stdlib.StrRPos(string(args[0].(vm.String)), string(args[1].(vm.String))))

	if res == -1 {
		return vm.Bool(false)
	}

	return res
}

func strripos(_ vm.Context, args ...vm.Value) vm.Value {
	res := vm.Int(stdlib.StrRIPos(string(args[0].(vm.String)), string(args[1].(vm.String))))

	if res == -1 {
		return vm.Bool(false)
	}

	return res
}

func strrev(_ vm.Context, args ...vm.Value) vm.String {
	return vm.String(stdlib.StrRev(string(args[0].(vm.String))))
}

func nl2br(_ vm.Context, args ...vm.Value) vm.String {
	return vm.String(stdlib.Nl2Br(string(args[0].(vm.String))))
}

func basename(_ vm.Context, args ...vm.Value) vm.String {
	var trimSuffix string

	if args[1] != nil {
		trimSuffix = string(args[1].(vm.String))
	}

	return vm.String(stdlib.Basename(string(args[0].(vm.String)), trimSuffix))
}

func dirname(_ vm.Context, args ...vm.Value) vm.String {
	return vm.String(stdlib.Dirname(string(args[0].(vm.String))))
}

const (
	PathinfoDirname = vm.Int(1 << iota)
	PathinfoBasename
	PathinfoExtension
	PathinfoFilename
	PathinfoAll = PathinfoDirname | PathinfoBasename | PathinfoExtension | PathinfoFilename
)

func pathinfo(_ vm.Context, args ...vm.Value) vm.Array {
	path := string(args[0].(vm.String))
	flags := args[1].(vm.Int)

	res := vm.Array{}

	if flags&PathinfoDirname > 0 {
		res[vm.String("dirname")] = vm.String(stdlib.Dirname(path))
	}

	if flags&PathinfoBasename > 0 {
		res[vm.String("basename")] = vm.String(stdlib.Basename(path, ""))
	}

	if flags&PathinfoExtension > 0 {
		res[vm.String("extension")] = vm.String(stdlib.Ext(path))
	}

	if flags&PathinfoFilename > 0 {
		res[vm.String("filename")] = vm.String(strings.TrimPrefix(path, stdlib.Dirname(path)))
	}

	return res
}

func stripslashes(_ vm.Context, args ...vm.Value) vm.String {
	return vm.String(stdlib.StripSlashes(string(args[0].(vm.String))))
}

func stripcslashes(_ vm.Context, args ...vm.Value) vm.String {
	return vm.String(stdlib.StripCSlashes(string(args[0].(vm.String))))
}

func strstr(_ vm.Context, args ...vm.Value) vm.Value {
	haystack := args[0].(vm.String)
	needle := args[1].(vm.String)

	if res, found := stdlib.StrStr(string(haystack), string(needle), args[2] != nil && bool(args[2].(vm.Bool))); found {
		return vm.String(res)
	}

	return vm.Bool(false)
}

func stristr(_ vm.Context, args ...vm.Value) vm.Value {
	haystack := args[0].(vm.String)
	needle := args[1].(vm.String)

	if res, found := stdlib.StrIStr(string(haystack), string(needle), args[2] != nil && bool(args[2].(vm.Bool))); found {
		return vm.String(res)
	}

	return vm.Bool(false)
}

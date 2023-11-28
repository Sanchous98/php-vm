package std

import (
	"github.com/Sanchous98/go-di/v2"
	"php-vm/internal/app"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
)

var Ext = compiler.Extension{
	Name:    "std",
	Version: "8.2.0",
	Functions: map[string]vm.Callable{
		"bin2hex":       vm.NewBuiltInFunction(bin2hex, vm.Arg{Name: "bin", Type: vm.StringType}),
		"sleep":         vm.NewBuiltInFunction(sleep, vm.Arg{Name: "t", Type: vm.IntType}),
		"usleep":        vm.NewBuiltInFunction(usleep, vm.Arg{Name: "t", Type: vm.IntType}),
		"strtoupper":    vm.NewBuiltInFunction(strtoupper, vm.Arg{Name: "str", Type: vm.StringType}),
		"strtolower":    vm.NewBuiltInFunction(strtolower, vm.Arg{Name: "str", Type: vm.StringType}),
		"strpos":        vm.NewBuiltInFunction(strpos, vm.Arg{Name: "str", Type: vm.StringType}, vm.Arg{Name: "sub", Type: vm.StringType}),
		"stripos":       vm.NewBuiltInFunction(stripos, vm.Arg{Name: "str", Type: vm.StringType}, vm.Arg{Name: "sub", Type: vm.StringType}),
		"strrpos":       vm.NewBuiltInFunction(strrpos, vm.Arg{Name: "str", Type: vm.StringType}, vm.Arg{Name: "sub", Type: vm.StringType}),
		"strripos":      vm.NewBuiltInFunction(strripos, vm.Arg{Name: "str", Type: vm.StringType}, vm.Arg{Name: "sub", Type: vm.StringType}),
		"strrev":        vm.NewBuiltInFunction(strrev, vm.Arg{Name: "str", Type: vm.StringType}),
		"nl2br":         vm.NewBuiltInFunction(nl2br, vm.Arg{Name: "str", Type: vm.StringType}),
		"basename":      vm.NewBuiltInFunction(basename, vm.Arg{Name: "str", Type: vm.StringType}),
		"dirname":       vm.NewBuiltInFunction(dirname, vm.Arg{Name: "str", Type: vm.StringType}),
		"pathinfo":      vm.NewBuiltInFunction(pathinfo, vm.Arg{Name: "str", Type: vm.StringType}, vm.Arg{Name: "flags", Type: vm.IntType, Default: PathinfoAll}),
		"stripslashes":  vm.NewBuiltInFunction(stripslashes, vm.Arg{Name: "str", Type: vm.StringType}),
		"stripcslashes": vm.NewBuiltInFunction(stripcslashes, vm.Arg{Name: "str", Type: vm.StringType}),
		"strstr":        vm.NewBuiltInFunction(strstr, vm.Arg{Name: "str", Type: vm.StringType}, vm.Arg{Name: "sub", Type: vm.StringType}),
		"stristr":       vm.NewBuiltInFunction(stristr, vm.Arg{Name: "str", Type: vm.StringType}, vm.Arg{Name: "sub", Type: vm.StringType}),
		"microtime":     vm.NewBuiltInFunction(microtime, vm.Arg{Name: "as_float", Type: vm.BoolType, Default: vm.Bool(false)}),
		"var_dump":      vm.NewBuiltInFunction(varDump, vm.Arg{Name: "value"}, vm.Arg{Name: "values", Variadic: true}),

		"count": vm.NewBuiltInFunction(count, vm.Arg{Name: "value", Type: vm.ArrayType}),
	},
	Constants: map[string]vm.Value{
		"PATHINFO_DIRNAME":   PathinfoDirname,
		"PATHINFO_BASENAME":  PathinfoBasename,
		"PATHINFO_EXTENSION": PathinfoExtension,
		"PATHINFO_FILENAME":  PathinfoFilename,
		"PATHINFO_ALL":       PathinfoAll,
		"PHP_EOL":            vm.String("\n"),
	},
}

func init() {
	app.App().Set(di.Service(&Ext), di.WithTags(compiler.PhpExtension))
}

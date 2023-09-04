package std

import (
	"github.com/Sanchous98/go-di/v2"
	"php-vm/internal/app"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
)

func init() {
	app.App().Set(di.Service(&compiler.Extension{
		Name:    "std",
		Version: "7.0.0",
		Functions: map[string]vm.Callable{
			"bin2hex":       vm.NewBuiltInFunction(bin2hex, 1),
			"sleep":         vm.NewBuiltInFunction(sleep, 1),
			"usleep":        vm.NewBuiltInFunction(usleep, 1),
			"strtoupper":    vm.NewBuiltInFunction(strtoupper, 1),
			"strtolower":    vm.NewBuiltInFunction(strtolower, 1),
			"strpos":        vm.NewBuiltInFunction(strpos, 1),
			"stripos":       vm.NewBuiltInFunction(stripos, 1),
			"strrpos":       vm.NewBuiltInFunction(strrpos, 1),
			"strripos":      vm.NewBuiltInFunction(strripos, 1),
			"strrev":        vm.NewBuiltInFunction(strrev, 1),
			"nl2br":         vm.NewBuiltInFunction(nl2br, 1),
			"basename":      vm.NewBuiltInFunction(basename, 1),
			"dirname":       vm.NewBuiltInFunction(dirname, 1),
			"pathinfo":      vm.NewBuiltInFunction(pathinfo, 1),
			"stripslashes":  vm.NewBuiltInFunction(stripslashes, 1),
			"stripcslashes": vm.NewBuiltInFunction(stripcslashes, 1),
			"strstr":        vm.NewBuiltInFunction(strstr, 1),
			"stristr":       vm.NewBuiltInFunction(stristr, 1),
			"microtime":     vm.NewBuiltInFunction(microtime, 1),
		},
		Constants: map[string]vm.Value{
			"PATHINFO_DIRNAME":   PathinfoDirname,
			"PATHINFO_BASENAME":  PathinfoBasename,
			"PATHINFO_EXTENSION": PathinfoExtension,
			"PATHINFO_FILENAME":  PathinfoFilename,
			"PATHINFO_ALL":       PathinfoAll,
		},
	}), di.WithTags(compiler.PhpExtension))
}

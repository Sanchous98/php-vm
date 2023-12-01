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
		"bin2hex":       vm.NewBuiltInFunction(bin2hex, "bin2hex"),
		"sleep":         vm.NewBuiltInFunction(sleep, "sleep"),
		"usleep":        vm.NewBuiltInFunction(usleep, "usleep"),
		"strtoupper":    vm.NewBuiltInFunction(strtoupper, "strtoupper"),
		"strtolower":    vm.NewBuiltInFunction(strtolower, "strtolower"),
		"strpos":        vm.NewBuiltInFunction(strpos, "strpos"),
		"stripos":       vm.NewBuiltInFunction(stripos, "stripos"),
		"strrpos":       vm.NewBuiltInFunction(strrpos, "strrpos"),
		"strripos":      vm.NewBuiltInFunction(strripos, "strripos"),
		"strrev":        vm.NewBuiltInFunction(strrev, "strrev"),
		"nl2br":         vm.NewBuiltInFunction(nl2br, "nl2br"),
		"basename":      vm.NewBuiltInFunction(basename, "basename"),
		"dirname":       vm.NewBuiltInFunction(dirname, "dirname"),
		"pathinfo":      vm.NewBuiltInFunction(pathinfo, "pathinfo"),
		"stripslashes":  vm.NewBuiltInFunction(stripslashes, "stripslashes"),
		"stripcslashes": vm.NewBuiltInFunction(stripcslashes, "stripcslashes"),
		"strstr":        vm.NewBuiltInFunction(strstr, "strstr"),
		"stristr":       vm.NewBuiltInFunction(stristr, "stristr"),
		"microtime":     vm.NewBuiltInFunction(microtime, "microtime"),
		"var_dump":      vm.NewBuiltInFunction(varDump, "var_dump"),

		"count": vm.NewBuiltInFunction(count, "count"),
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

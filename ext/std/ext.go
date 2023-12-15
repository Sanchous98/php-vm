package std

import (
	"github.com/Sanchous98/go-di/v2"
	"php-vm/internal/app"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
)

var Ext = compiler.Extension{
	Name:    "std",
	Version: "8.3.0",
	Functions: map[string]vm.Function{
		"bin2hex":       {Executable: vm.BuiltInFunction[vm.String](bin2hex), FuncName: "bin2hex"},
		"sleep":         {Executable: vm.BuiltInFunction[vm.Int](sleep), FuncName: "sleep"},
		"usleep":        {Executable: vm.BuiltInFunction[vm.Null](usleep), FuncName: "usleep"},
		"strtoupper":    {Executable: vm.BuiltInFunction[vm.String](strtoupper), FuncName: "strtoupper"},
		"strtolower":    {Executable: vm.BuiltInFunction[vm.String](strtolower), FuncName: "strtolower"},
		"strpos":        {Executable: vm.BuiltInFunction[vm.Value](strpos), FuncName: "strpos"},
		"stripos":       {Executable: vm.BuiltInFunction[vm.Value](stripos), FuncName: "stripos"},
		"strrpos":       {Executable: vm.BuiltInFunction[vm.Value](strrpos), FuncName: "strrpos"},
		"strripos":      {Executable: vm.BuiltInFunction[vm.Value](strripos), FuncName: "strripos"},
		"strrev":        {Executable: vm.BuiltInFunction[vm.String](strrev), FuncName: "strrev"},
		"nl2br":         {Executable: vm.BuiltInFunction[vm.String](nl2br), FuncName: "nl2br"},
		"basename":      {Executable: vm.BuiltInFunction[vm.String](basename), FuncName: "basename"},
		"dirname":       {Executable: vm.BuiltInFunction[vm.String](dirname), FuncName: "dirname"},
		"stripslashes":  {Executable: vm.BuiltInFunction[vm.String](stripslashes), FuncName: "stripslashes"},
		"stripcslashes": {Executable: vm.BuiltInFunction[vm.String](stripcslashes), FuncName: "stripcslashes"},
		"strstr":        {Executable: vm.BuiltInFunction[vm.Value](strstr), FuncName: "strstr"},
		"stristr":       {Executable: vm.BuiltInFunction[vm.Value](stristr), FuncName: "stristr"},
		"microtime":     {Executable: vm.BuiltInFunction[vm.Value](microtime), FuncName: "microtime"},
		"var_dump":      {Executable: vm.BuiltInFunction[vm.Value](varDump), FuncName: "var_dump"},
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

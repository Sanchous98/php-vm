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
			"bin2hex": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   bin2hex,
			},
			"sleep": vm.BuiltInFunction[vm.Int]{
				Args: 1,
				Fn:   sleep,
			},
			"usleep": vm.BuiltInFunction[vm.Null]{
				Args: 1,
				Fn:   usleep,
			},
			"strtoupper": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   strtoupper,
			},
			"strtolower": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   strtolower,
			},
			"strpos": vm.BuiltInFunction[vm.Value]{
				Args: 1,
				Fn:   strpos,
			},
			"stripos": vm.BuiltInFunction[vm.Value]{
				Args: 1,
				Fn:   stripos,
			},
			"strrpos": vm.BuiltInFunction[vm.Value]{
				Args: 1,
				Fn:   strrpos,
			},
			"strripos": vm.BuiltInFunction[vm.Value]{
				Args: 1,
				Fn:   strripos,
			},
			"strrev": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   strrev,
			},
			"nl2br": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   nl2br,
			},
			"basename": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   basename,
			},
			"dirname": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   dirname,
			},
			"pathinfo": vm.BuiltInFunction[vm.Array]{
				Args: 1,
				Fn:   pathinfo,
			},
			"stripslashes": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   stripslashes,
			},
			"stripcslashes": vm.BuiltInFunction[vm.String]{
				Args: 1,
				Fn:   stripcslashes,
			},
			"strstr": vm.BuiltInFunction[vm.Value]{
				Args: 1,
				Fn:   strstr,
			},
			"stristr": vm.BuiltInFunction[vm.Value]{
				Args: 1,
				Fn:   stristr,
			},
			"microtime": vm.BuiltInFunction[vm.Value]{
				Args: 1,
				Fn:   microtime,
			},
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

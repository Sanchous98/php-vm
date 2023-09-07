package compiler

import (
	"github.com/Sanchous98/go-di/v2"
	"php-vm/internal/app"
)

const PhpExtension = "php.extension"

type Extensions struct {
	Exts []Extension `inject:"php.extension"`
}

func init() {
	app.App().Set(di.Default(new(Extensions)))
	app.App().Set(di.Constructor[*Compiler](NewCompiler))
}

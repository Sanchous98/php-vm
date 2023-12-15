package compiler

import (
	"php-vm/internal/vm"
)

type Extension struct {
	Name, Version string
	Functions     map[string]vm.Function
	Constants     map[string]vm.Value
}

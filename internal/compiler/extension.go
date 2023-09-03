package compiler

import "php-vm/internal/vm"

type Extension struct {
	Name, Version string
	Functions     map[string]vm.Callable
	Constants     map[string]vm.Value
}

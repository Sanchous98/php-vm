package main

import (
	"fmt"
	"php-vm/internal/vm"
)

var stack [2]vm.Value

func main() {
	stack[0] = vm.Int(0)
	stack[1] = vm.NewRef(&stack[0])

	if stack[1].IsRef() {
		*stack[1].Deref() = vm.Int(1)
	}

	fmt.Println(stack[0])
}

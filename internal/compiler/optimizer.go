package compiler

import (
	"php-vm/internal/vm"
)

func Optimizer(bytecode vm.Instructions) vm.Instructions {
	//bytecode = JoinPops(bytecode)
	return removeNops(bytecode)
}

func JoinPops(bytecode vm.Instructions) vm.Instructions {
	return vm.Reduce(bytecode, func(prev vm.Instructions, operator vm.Operator, operands ...int) vm.Instructions {
		if operator == vm.OpPop && vm.Operator(prev[len(prev)-1]) == vm.OpPop {
			prev[len(prev)-1] = uint64(vm.OpPop2)
		} else {
			prev = append(prev, uint64(operator))

			for _, operand := range operands {
				prev = append(prev, uint64(operand))
			}
		}

		return prev
	}, nil)
}

// NOOP => _
func removeNops(bytecode vm.Instructions) vm.Instructions {
	return vm.Reduce(bytecode, func(prev vm.Instructions, operator vm.Operator, operands ...int) vm.Instructions {
		if operator != vm.OpNoop {
			prev = append(prev, uint64(operator))

			for _, operand := range operands {
				prev = append(prev, uint64(operand))
			}
		}

		return prev
	}, nil)
}

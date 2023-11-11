package compiler

import (
	"encoding/binary"
	"php-vm/internal/vm"
)

func Optimizer(bytecode vm.Bytecode) vm.Bytecode {
	//bytecode = JoinPops(bytecode)
	return removeNops(bytecode)
}

func JoinPops(bytecode vm.Bytecode) vm.Bytecode {
	return vm.Reduce(bytecode, func(prev vm.Bytecode, operator vm.Operator, operands ...int) vm.Bytecode {
		if operator == vm.OpPop && vm.Operator(binary.NativeEndian.Uint64(prev[len(prev)-8:])) == vm.OpPop {
			binary.NativeEndian.PutUint64(prev[len(prev)-8:], uint64(vm.OpPop2))
		} else {
			prev = binary.NativeEndian.AppendUint64(prev, uint64(operator))

			for _, operand := range operands {
				prev = binary.NativeEndian.AppendUint64(prev, uint64(operand))
			}
		}

		return prev
	}, nil)
}

// NOOP => _
func removeNops(bytecode vm.Bytecode) vm.Bytecode {
	return vm.Reduce(bytecode, func(prev vm.Bytecode, operator vm.Operator, operands ...int) vm.Bytecode {
		if operator != vm.OpNoop {
			prev = binary.NativeEndian.AppendUint64(prev, uint64(operator))

			for _, operand := range operands {
				prev = binary.NativeEndian.AppendUint64(prev, uint64(operand))
			}
		}

		return prev
	}, nil)
}

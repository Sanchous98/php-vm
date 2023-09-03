package compiler

import (
	"encoding/binary"
	"php-vm/internal/vm"
)

func Optimizer(bytecode vm.Bytecode) vm.Bytecode {
	bytecode = removeNops(bytecode)
	//bytecode = removeMultipleReturns(bytecode)
	return bytecode
}

func removeNops(bytecode vm.Bytecode) vm.Bytecode {
	return vm.Reduce(bytecode, func(prev vm.Bytecode, operator vm.Operator, operands ...int) vm.Bytecode {
		if operator != vm.OpNoop {
			prev = binary.NativeEndian.AppendUint64(prev, uint64(operator))

			for _, operand := range operands {
				prev = binary.NativeEndian.AppendUint64(prev, uint64(operand))
			}
		}

		return bytecode
	}, nil)
}

//func removeMultipleReturns(bytecode vm.Bytecode) vm.Bytecode {
//	return vm.Reduce(bytecode, func(prev vm.Bytecode, operator vm.Operator, operands ...byte) vm.Bytecode {
//		switch operator {
//
//		}
//		if operator == vm.OpReturn || operator == vm.OpReturnValue {
//			if prev[len(prev)-1]
//		}
//	}, nil)
//}

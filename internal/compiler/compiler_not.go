package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/internal/vm"
)

func (c *Compiler) ExprBitwiseNot(n *ast.ExprBitwiseNot) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpBwNot)<<32)
}

func (c *Compiler) ExprBooleanNot(n *ast.ExprBooleanNot) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpNot)<<32)
}

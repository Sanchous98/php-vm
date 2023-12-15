package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/internal/vm"
)

func (c *Compiler) ExprCastArray(n *ast.ExprCastArray) {
	n.Expr.Accept(c)
	c.castOp(vm.ArrayType)
}

func (c *Compiler) ExprCastBool(n *ast.ExprCastBool) {
	n.Expr.Accept(c)
	c.castOp(vm.BoolType)
}

func (c *Compiler) ExprCastDouble(n *ast.ExprCastDouble) {
	n.Expr.Accept(c)
	c.castOp(vm.FloatType)
}

func (c *Compiler) ExprCastInt(n *ast.ExprCastInt) {
	n.Expr.Accept(c)
	c.castOp(vm.IntType)
}

func (c *Compiler) ExprCastObject(n *ast.ExprCastObject) {
	n.Expr.Accept(c)
	c.castOp(vm.ObjectType)
}

func (c *Compiler) ExprCastString(n *ast.ExprCastString) {
	n.Expr.Accept(c)
	c.castOp(vm.StringType)
}

func (c *Compiler) ExprCastUnset(n *ast.ExprCastUnset) {
	n.Expr.Accept(c)
	c.castOp(vm.NullType)
}

func (c *Compiler) castOp(_type vm.Type) {
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpCast)<<32+uint64(_type))
}

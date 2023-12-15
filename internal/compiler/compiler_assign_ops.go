package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/internal/vm"
)

const additionalOp = uint64(^uint32(0))

func (c *Compiler) ExprAssign(n *ast.ExprAssign) {
	switch n.Var.(type) {
	case *ast.ExprArrayDimFetch:
		c.arrayWriteMode[n.Var] = true
		n.Var.Accept(c)
		n.Expr.Accept(c)
		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpAssignRef)<<32, uint64(vm.OpPop)<<32)
	case *ast.ExprPropertyFetch:
		// TODO:
	default:
		n.Expr.Accept(c)
		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpAssign)<<32+uint64(c.context.Var(c.context.Resolve(n.Var, VariableAliasType))))
	}
}

func (c *Compiler) ExprAssignReference(n *ast.ExprAssignReference) {
	n.Expr.Accept(c)
	c.assignOp(vm.OpLoadRef)

	n.Var.Accept(c)
	c.assignOp(vm.OpAssign)
}

func (c *Compiler) ExprAssignBitwiseAnd(n *ast.ExprAssignBitwiseAnd) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignBwAnd)
}

func (c *Compiler) ExprAssignBitwiseOr(n *ast.ExprAssignBitwiseOr) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignBwOr)
}

func (c *Compiler) ExprAssignBitwiseXor(n *ast.ExprAssignBitwiseXor) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignBwXor)
}

func (c *Compiler) ExprAssignCoalesce(n *ast.ExprAssignCoalesce) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignCoalesce)
}

func (c *Compiler) ExprAssignConcat(n *ast.ExprAssignConcat) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignConcat)
}

func (c *Compiler) ExprAssignPow(n *ast.ExprAssignPow) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignPow)
}

func (c *Compiler) ExprAssignShiftLeft(n *ast.ExprAssignShiftLeft) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignShiftLeft)
}

func (c *Compiler) ExprAssignShiftRight(n *ast.ExprAssignShiftRight) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignShiftRight)
}

func (c *Compiler) ExprAssignDiv(n *ast.ExprAssignDiv) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignDiv)
}

func (c *Compiler) ExprAssignMinus(n *ast.ExprAssignMinus) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignSub)
}

func (c *Compiler) ExprAssignMod(n *ast.ExprAssignMod) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignMod)
}

func (c *Compiler) ExprAssignMul(n *ast.ExprAssignMul) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignMul)
}

func (c *Compiler) ExprAssignPlus(n *ast.ExprAssignPlus) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.assignOp(vm.OpAssignAdd)
}

func (c *Compiler) ExprPostInc(n *ast.ExprPostInc) {
	n.Var.Accept(c)
	c.assignOp(vm.OpPostIncrement)
}

func (c *Compiler) ExprPreInc(n *ast.ExprPreInc) {
	n.Var.Accept(c)
	c.assignOp(vm.OpPreIncrement)
}

func (c *Compiler) ExprPostDec(n *ast.ExprPostDec) {
	n.Var.Accept(c)
	c.assignOp(vm.OpPostDecrement)
}

func (c *Compiler) ExprPreDec(n *ast.ExprPreDec) {
	n.Var.Accept(c)
	c.assignOp(vm.OpPreDecrement)
}

func (c *Compiler) assignOp(op vm.Operator) {
	(*c.context.Bytecode())[len(*c.context.Bytecode())-1] = uint64(op)<<32 + (*c.context.Bytecode())[len(*c.context.Bytecode())-1]&additionalOp
}

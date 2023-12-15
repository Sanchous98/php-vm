package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/internal/vm"
)

func (c *Compiler) ExprBinaryBooleanAnd(n *ast.ExprBinaryBooleanAnd) {
	panic("not implemented")
}

func (c *Compiler) ExprBinaryBooleanOr(n *ast.ExprBinaryBooleanOr) {
	panic("not implemented")
}

func (c *Compiler) ExprBinaryCoalesce(n *ast.ExprBinaryCoalesce) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpCoalesce)
}

func (c *Compiler) ExprBinaryConcat(n *ast.ExprBinaryConcat) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpConcat)
}

func (c *Compiler) ExprBinaryDiv(n *ast.ExprBinaryDiv) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpDiv)
}

func (c *Compiler) ExprBinaryEqual(n *ast.ExprBinaryEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpEqual)
}

func (c *Compiler) ExprBinaryGreater(n *ast.ExprBinaryGreater) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpGreater)
}

func (c *Compiler) ExprBinaryGreaterOrEqual(n *ast.ExprBinaryGreaterOrEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpGreaterOrEqual)
}

func (c *Compiler) ExprBinaryLogicalAnd(n *ast.ExprBinaryLogicalAnd) {
	panic("not implemented")
}

func (c *Compiler) ExprBinaryLogicalOr(n *ast.ExprBinaryLogicalOr) {
	panic("not implemented")
}

func (c *Compiler) ExprBinaryLogicalXor(n *ast.ExprBinaryLogicalXor) {
	panic("not implemented")
}

func (c *Compiler) ExprBinaryMod(n *ast.ExprBinaryMod) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpMod)
}

func (c *Compiler) ExprBinaryMul(n *ast.ExprBinaryMul) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpMul)
}

func (c *Compiler) ExprBinaryNotEqual(n *ast.ExprBinaryNotEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpNotEqual)
}

func (c *Compiler) ExprBinaryNotIdentical(n *ast.ExprBinaryNotIdentical) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpNotIdentical)
}

func (c *Compiler) ExprBinaryPow(n *ast.ExprBinaryPow) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpPow)
}

func (c *Compiler) ExprBinaryShiftLeft(n *ast.ExprBinaryShiftLeft) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpShiftLeft)
}

func (c *Compiler) ExprBinaryShiftRight(n *ast.ExprBinaryShiftRight) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpShiftRight)
}

func (c *Compiler) ExprBinarySmaller(n *ast.ExprBinarySmaller) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpLess)
}

func (c *Compiler) ExprBinarySmallerOrEqual(n *ast.ExprBinarySmallerOrEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpLessOrEqual)
}

func (c *Compiler) ExprBinarySpaceship(n *ast.ExprBinarySpaceship) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpCompare)
}

func (c *Compiler) ExprBinaryIdentical(n *ast.ExprBinaryIdentical) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpIdentical)
}

func (c *Compiler) ExprBinaryMinus(n *ast.ExprBinaryMinus) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpSub)
}

func (c *Compiler) ExprBinaryPlus(n *ast.ExprBinaryPlus) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpAdd)
}

func (c *Compiler) ExprBinaryBitwiseAnd(n *ast.ExprBinaryBitwiseAnd) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpBwAnd)
}

func (c *Compiler) ExprBinaryBitwiseOr(n *ast.ExprBinaryBitwiseOr) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpBwOr)
}

func (c *Compiler) ExprBinaryBitwiseXor(n *ast.ExprBinaryBitwiseXor) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.binaryOp(vm.OpBwXor)
}

func (c *Compiler) binaryOp(op vm.Operator) {
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(op)<<32)
}

package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/internal/vm"
)

func (c *Compiler) StmtClass(n *ast.StmtClass) {
	c.context.Resolve(n.Name, "class")
	n.Extends.Accept(c)

	for _, modifiers := range n.Modifiers {
		modifiers.Accept(c)
	}

	for _, impl := range n.Implements {
		impl.Accept(c)
	}

	for _, arg := range n.Args {
		arg.Accept(c)
	}

	for _, stmt := range n.Stmts {
		stmt.Accept(c)
	}
}

func (c *Compiler) StmtConstant(n *ast.StmtConstant) {
	n.Expr.Accept(c)

	if (*c.context.Bytecode())[len(*c.context.Bytecode())-2]>>32 == uint64(vm.OpConst) {
		*c.context.Bytecode() = (*c.context.Bytecode())[:len(*c.context.Bytecode())-2]
	}

	name := c.context.Resolve(n.Name, ConstantAliasType)
	c.global.NamedConstants[name] = c.global.Constants[n.Expr]
}

func (c *Compiler) StmtConstList(n *ast.StmtConstList) {
	for _, stmt := range n.Consts {
		stmt.Accept(c)
	}
}

func (c *Compiler) StmtDeclare(n *ast.StmtDeclare) {
	for _, constant := range n.Consts {
		constant.Accept(c)
	}

	n.Stmt.Accept(c)
}

func (c *Compiler) StmtFunction(n *ast.StmtFunction) {
	ctx := c.context.Child(c.context.Resolve(n.Name, FunctionAliasType))
	c.context = ctx
	c.contexts = append(c.contexts, ctx)

	for _, param := range n.Params {
		param.Accept(c)
	}

	for _, stmt := range n.Stmts {
		stmt.Accept(c)
	}

	switch (*c.context.Bytecode())[len(*c.context.Bytecode())-1] >> 32 {
	case uint64(vm.OpReturn), uint64(vm.OpReturnValue):
	default:
		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpReturn)<<32)
	}

	if n.ReturnType != nil {
		n.ReturnType.Accept(c)
	}

	c.context = c.context.Parent()
}

func (c *Compiler) StmtHaltCompiler(*ast.StmtHaltCompiler) {}

func (c *Compiler) StmtIf(n *ast.StmtIf) {
	n.Cond.Accept(c)

	pos := len(*c.context.Bytecode())
	n.Stmt.Accept(c)

	goTo := len(*c.context.Bytecode()) + 1

	end := make([]uint64, len((*c.context.Bytecode())[pos:]))
	copy(end, (*c.context.Bytecode())[pos:])

	*c.context.Bytecode() = append((*c.context.Bytecode())[:pos], uint64(vm.OpJumpFalse)<<32+uint64(goTo))
	*c.context.Bytecode() = append(*c.context.Bytecode(), end...)

	for _, elif := range n.ElseIf {
		elif.Accept(c)
	}

	if n.Else != nil {
		n.Else.Accept(c)
	}
}

func (c *Compiler) StmtElseIf(n *ast.StmtElseIf) {
	n.Cond.Accept(c)

	pos := len(*c.context.Bytecode())
	n.Stmt.Accept(c)

	goTo := len(*c.context.Bytecode()) + 1

	end := make([]uint64, len((*c.context.Bytecode())[pos:]))
	copy(end, (*c.context.Bytecode())[pos:])

	*c.context.Bytecode() = append((*c.context.Bytecode())[:pos], uint64(vm.OpJumpFalse)<<32+uint64(goTo))
	*c.context.Bytecode() = append(*c.context.Bytecode(), end...)
}

func (c *Compiler) StmtElse(n *ast.StmtElse) {
	n.Stmt.Accept(c)
}

func (c *Compiler) StmtNop(*ast.StmtNop) {
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpNoop)<<32)
}

func (c *Compiler) StmtReturn(n *ast.StmtReturn) {
	if n.Expr == nil {
		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpReturn)<<32)
	} else {
		n.Expr.Accept(c)

		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpReturnValue)<<32)
	}
}

func (c *Compiler) StmtStmtList(n *ast.StmtStmtList) {
	for _, stmt := range n.Stmts {
		stmt.Accept(c)
	}
}

func (c *Compiler) StmtExpression(n *ast.StmtExpression) {
	n.Expr.Accept(c)

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpPop)<<32)
}

func (c *Compiler) StmtFor(n *ast.StmtFor) {
	for _, expr := range n.Init {
		expr.Accept(c)
	}

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpPop)<<32)
	condPos := len(*c.context.Bytecode())

	for _, cond := range n.Cond {
		cond.Accept(c)
	}

	pos := len(*c.context.Bytecode())
	n.Stmt.Accept(c)

	for _, loop := range n.Loop {
		loop.Accept(c)
	}

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpPop)<<32, uint64(vm.OpJump)<<32+uint64(condPos))

	goTo := len(*c.context.Bytecode()) + 1

	end := make([]uint64, len((*c.context.Bytecode())[pos:]))
	copy(end, (*c.context.Bytecode())[pos:])

	*c.context.Bytecode() = append((*c.context.Bytecode())[:pos], uint64(vm.OpJumpFalse)<<32+uint64(goTo))
	*c.context.Bytecode() = append(*c.context.Bytecode(), end...)
}

func (c *Compiler) StmtForeach(n *ast.StmtForeach) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpForEachInit)<<32)
	pos := len(*c.context.Bytecode())
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpForEachValid)<<32)
	iter := len(*c.context.Bytecode())
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpJumpFalse)<<32)

	n.Var.Accept(c)

	if n.AmpersandTkn == nil {
		(*c.context.Bytecode())[len(*c.context.Bytecode())-1] = uint64(vm.OpForEachValue) << 32
	} else {
		(*c.context.Bytecode())[len(*c.context.Bytecode())-1] = uint64(vm.OpForEachValueRef) << 32
	}

	if n.Key != nil {
		n.Key.Accept(c)
		(*c.context.Bytecode())[len(*c.context.Bytecode())-1] = uint64(vm.OpForEachKey)<<32 + (*c.context.Bytecode())[len(*c.context.Bytecode())-1]&additionalOp
	}

	n.Stmt.Accept(c)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpForEachNext)<<32, uint64(vm.OpJump)<<32+uint64(pos), uint64(vm.OpPop)<<32)
	(*c.context.Bytecode())[iter] += uint64(len(*c.context.Bytecode()))
}

func (c *Compiler) StmtWhile(n *ast.StmtWhile) {
	cond := len(*c.context.Bytecode())
	n.Cond.Accept(c)
	pos := len(*c.context.Bytecode())
	n.Stmt.Accept(c)

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpJump)<<32+uint64(cond))

	goTo := len(*c.context.Bytecode()) + 1

	end := make([]uint64, len((*c.context.Bytecode())[pos:]))
	copy(end, (*c.context.Bytecode())[pos:])

	*c.context.Bytecode() = append((*c.context.Bytecode())[:pos], uint64(vm.OpJumpFalse)<<32+uint64(goTo))
	*c.context.Bytecode() = append(*c.context.Bytecode(), end...)
}

func (c *Compiler) StmtDo(n *ast.StmtDo) {
	pos := len(*c.context.Bytecode())
	n.Stmt.Accept(c)
	n.Cond.Accept(c)

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpJumpFalse)<<32+uint64(pos))
}

func (c *Compiler) StmtGoto(n *ast.StmtGoto) {
	name := c.context.Resolve(n.Label, "")
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpJump))
	c.context.AddLabel(name, uint64(len(*c.context.Bytecode())))
}

func (c *Compiler) StmtLabel(n *ast.StmtLabel) {
	label := c.context.Resolve(n.Name, "")
	(*c.context.Bytecode())[c.context.FindLabel(label)] = uint64(len(*c.context.Bytecode()))
}

func (c *Compiler) StmtEcho(n *ast.StmtEcho) {
	for _, expr := range n.Exprs {
		expr.Accept(c)
	}

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpEcho)<<32+uint64(len(n.Exprs)))
}

func (c *Compiler) StmtUnset(*ast.StmtUnset) {
	panic("not implemented")
}

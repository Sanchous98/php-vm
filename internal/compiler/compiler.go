package compiler

import (
	"encoding/binary"
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/nsresolver"
	"php-vm/internal/compiler/internal"
	"php-vm/internal/vm"
	"slices"
	"strconv"
	"unsafe"
)

var builtInTypeAsserts = map[string]vm.Type{
	"int":    vm.IntType,
	"float":  vm.FloatType,
	"bool":   vm.BoolType,
	"array":  vm.ArrayType,
	"object": vm.ObjectType,
}

const (
	FunctionAliasType = "function"
	ConstantAliasType = "const"
	VariableAliasType = "variable"
)

type Compiler struct {
	visitor.Null

	extensions []Extension

	contexts       []*internal.FunctionContext
	global         *internal.GlobalContext
	context        internal.Context
	ctx            *vm.GlobalContext
	arrayWriteMode map[ast.Vertex]bool
}

func (c *Compiler) Root(n *ast.Root) {
	for _, stmt := range n.Stmts {
		stmt.Accept(c)

		if _, ok := stmt.(*ast.StmtReturn); ok {
			break
		}
	}

	switch binary.NativeEndian.Uint64((*c.context.Bytecode())[len(*c.context.Bytecode())-8:]) {
	case uint64(vm.OpReturn), uint64(vm.OpReturnValue):
	default:
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpReturn))
	}
}

func (c *Compiler) Parameter(n *ast.Parameter) {
	name := c.context.Resolve(n.Var, VariableAliasType)
	_type := c.context.Resolve(n.Type, "")
	c.context.Arg(name, _type, n.DefaultValue, n.AmpersandTkn != nil)
}

func (c *Compiler) Argument(n *ast.Argument) {
	n.Expr.Accept(c)
}

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

	if (*c.context.Bytecode())[len(*c.context.Bytecode())-2] == byte(vm.OpConst) {
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

	switch binary.NativeEndian.Uint64((*c.context.Bytecode())[len(*c.context.Bytecode())-8:]) {
	case uint64(vm.OpReturn), uint64(vm.OpReturnValue):
	default:
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpReturn))
	}

	if n.ReturnType != nil {
		n.ReturnType.Accept(c)
	}

	c.context = c.context.Parent()
}

func (c *Compiler) StmtIf(n *ast.StmtIf) {
	n.Cond.Accept(c)

	pos := len(*c.context.Bytecode())
	n.Stmt.Accept(c)

	goTo := (len(*c.context.Bytecode()) + 16) >> 3

	end := make([]byte, len((*c.context.Bytecode())[pos:]))
	copy(end, (*c.context.Bytecode())[pos:])

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64((*c.context.Bytecode())[:pos], uint64(vm.OpJumpFalse))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(goTo))
	*c.context.Bytecode() = append(*c.context.Bytecode(), end...)
}

func (c *Compiler) StmtNop(*ast.StmtNop) {
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpNoop))
}

func (c *Compiler) StmtReturn(n *ast.StmtReturn) {
	if n.Expr == nil {
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpReturn))
	} else {
		n.Expr.Accept(c)

		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpReturnValue))
	}
}

func (c *Compiler) StmtStmtList(n *ast.StmtStmtList) {
	for _, stmt := range n.Stmts {
		stmt.Accept(c)
	}
}

func (c *Compiler) StmtExpression(n *ast.StmtExpression) {
	n.Expr.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpPop))
}

func (c *Compiler) StmtFor(n *ast.StmtFor) {
	for _, expr := range n.Init {
		expr.Accept(c)
	}

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpPop))
	condPos := len(*c.context.Bytecode()) >> 3

	for _, cond := range n.Cond {
		cond.Accept(c)
	}

	pos := len(*c.context.Bytecode())
	n.Stmt.Accept(c)

	for _, loop := range n.Loop {
		loop.Accept(c)
	}

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpPop))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpJump))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(condPos))

	goTo := (len(*c.context.Bytecode()) + 16) >> 3

	end := make([]byte, len((*c.context.Bytecode())[pos:]))
	copy(end, (*c.context.Bytecode())[pos:])

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64((*c.context.Bytecode())[:pos], uint64(vm.OpJumpFalse))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(goTo))
	*c.context.Bytecode() = append(*c.context.Bytecode(), end...)
}

func (c *Compiler) StmtForeach(n *ast.StmtForeach) {
	if n.Key != nil {
		n.Key.Accept(c)
	}

	n.Var.Accept(c)
}

func (c *Compiler) StmtWhile(n *ast.StmtWhile) {
	cond := len(*c.context.Bytecode()) >> 3
	n.Cond.Accept(c)
	pos := len(*c.context.Bytecode())
	n.Stmt.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpJump))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(cond))

	goTo := (len(*c.context.Bytecode()) + 16) >> 3

	end := make([]byte, len((*c.context.Bytecode())[pos:]))
	copy(end, (*c.context.Bytecode())[pos:])

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64((*c.context.Bytecode())[:pos], uint64(vm.OpJumpFalse))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(goTo))
	*c.context.Bytecode() = append(*c.context.Bytecode(), end...)
}

func (c *Compiler) StmtDo(n *ast.StmtDo) {
	pos := len(*c.context.Bytecode()) >> 3
	n.Stmt.Accept(c)
	n.Cond.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpJumpFalse))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(pos))
}

func (c *Compiler) StmtGoto(n *ast.StmtGoto) {
	name := c.context.Resolve(n.Label, "")
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpJump))
	c.context.AddLabel(name, uint64(len(*c.context.Bytecode())))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpNoop))
}

func (c *Compiler) StmtLabel(n *ast.StmtLabel) {
	label := c.context.Resolve(n.Name, "")

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[c.context.FindLabel(label):], uint64(len(*c.context.Bytecode()))>>3)
}

func (c *Compiler) StmtEcho(n *ast.StmtEcho) {
	for _, expr := range n.Exprs {
		expr.Accept(c)
	}

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpEcho))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(len(n.Exprs)))
}

func (c *Compiler) StmtUnset(n *ast.StmtUnset) {
	for _, v := range n.Vars {
		v.Accept(c)
		switch v.(type) {
		case *ast.ExprArrayDimFetch:
			binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpArrayAccessWrite))
		case *ast.ExprPropertyFetch:
			binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpPropertyUnset))
		default:
			binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpUnset))
		}
	}
}

func (c *Compiler) ExprFunctionCall(n *ast.ExprFunctionCall) {
	name := c.context.Resolve(n.Function, FunctionAliasType)
	f := slices.IndexFunc(c.contexts, func(context *internal.FunctionContext) bool {
		return context.Name == name
	})

	if f >= 0 {
		for i, arg := range c.contexts[f].Args {
			if len(n.Args)-1 < i {
				if arg.Default != nil {
					arg.Default.Accept(c)
				}
				continue
			}

			n.Args[i].Accept(c)

			if arg.IsRef {
				binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpLoadRef))
			}

			if arg.Type != "" {
				if aT, ok := builtInTypeAsserts[arg.Type]; ok {
					*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpAssertType))
					*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(aT))
				}
			}
		}
	}

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCall))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(c.context.Function(name)))
}

func (c *Compiler) ExprVariable(n *ast.ExprVariable) {
	name := c.context.Resolve(n.Name, VariableAliasType)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpLoad))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(c.context.Var(name)))
}

func (c *Compiler) ExprConstFetch(n *ast.ExprConstFetch) {
	name := c.context.Resolve(n.Const, ConstantAliasType)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpConst))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(c.context.Constant(name)))
}

func (c *Compiler) ExprAssign(n *ast.ExprAssign) {
	switch n.Var.(type) {
	case *ast.ExprArrayDimFetch:
		c.arrayWriteMode[n.Var] = true
		n.Var.Accept(c)
		n.Expr.Accept(c)
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpAssignRef))
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpPop))
	default:
		n.Expr.Accept(c)
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpAssign))
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(c.context.Var(c.context.Resolve(n.Var, VariableAliasType))))
	}
}

func (c *Compiler) ExprAssignBitwiseAnd(n *ast.ExprAssignBitwiseAnd) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignBwAnd))
}

func (c *Compiler) ExprAssignBitwiseOr(n *ast.ExprAssignBitwiseOr) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignBwOr))
}

func (c *Compiler) ExprAssignBitwiseXor(n *ast.ExprAssignBitwiseXor) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignBwXor))
}

func (c *Compiler) ExprAssignCoalesce(*ast.ExprAssignCoalesce) {
	panic("not implemented")
}

func (c *Compiler) ExprAssignConcat(n *ast.ExprAssignConcat) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignConcat))
}

func (c *Compiler) ExprAssignPow(n *ast.ExprAssignPow) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignPow))
}

func (c *Compiler) ExprAssignShiftLeft(n *ast.ExprAssignShiftLeft) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignShiftLeft))
}

func (c *Compiler) ExprAssignShiftRight(n *ast.ExprAssignShiftRight) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignShiftRight))
}

func (c *Compiler) ExprAssignReference(n *ast.ExprAssignReference) {
	n.Expr.Accept(c)
	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpLoadRef))

	n.Var.Accept(c)
	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssign))
}

func (c *Compiler) ExprArray(n *ast.ExprArray) {
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpArrayNew))

	for _, item := range n.Items {
		item.Accept(c)
	}
}

func (c *Compiler) ExprArrayDimFetch(n *ast.ExprArrayDimFetch) {
	if n.Dim == nil {
		c.arrayWriteMode[n.Var] = true
		n.Var.Accept(c)
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpArrayAccessPush))
	} else {
		if c.arrayWriteMode[n] {
			switch n.Var.(type) {
			case *ast.ExprArrayDimFetch:
				c.arrayWriteMode[n.Var] = true
			}
			n.Var.Accept(c)
			n.Dim.Accept(c)
			*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpArrayAccessWrite))
		} else {
			n.Var.Accept(c)
			n.Dim.Accept(c)
			*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpArrayAccessRead))
		}
	}
}

func (c *Compiler) ExprArrayItem(n *ast.ExprArrayItem) {
	if n.Key == nil {
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpArrayAccessPush))
	} else {
		n.Key.Accept(c)
		*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpArrayAccessWrite))
	}
	n.Val.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpAssignRef))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpPop))
}

func (c *Compiler) ExprPostInc(n *ast.ExprPostInc) {
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpPostIncrement))
}

func (c *Compiler) ExprPreInc(n *ast.ExprPreInc) {
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpPreIncrement))
}

func (c *Compiler) ExprPostDec(n *ast.ExprPostDec) {
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpPostDecrement))
}

func (c *Compiler) ExprPreDec(n *ast.ExprPreDec) {
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpPreDecrement))
}

func (c *Compiler) ExprAssignDiv(n *ast.ExprAssignDiv) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignDiv))
}

func (c *Compiler) ExprAssignMinus(n *ast.ExprAssignMinus) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignSub))
}

func (c *Compiler) ExprAssignMod(n *ast.ExprAssignMod) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignMod))
}

func (c *Compiler) ExprAssignMul(n *ast.ExprAssignMul) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignMul))
}

func (c *Compiler) ExprAssignPlus(n *ast.ExprAssignPlus) {
	n.Expr.Accept(c)
	n.Var.Accept(c)

	binary.NativeEndian.PutUint64((*c.context.Bytecode())[len(*c.context.Bytecode())-16:], uint64(vm.OpAssignAdd))
}

func (c *Compiler) ExprBinaryIdentical(n *ast.ExprBinaryIdentical) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpIdentical))
}

func (c *Compiler) ExprBinaryMinus(n *ast.ExprBinaryMinus) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpSub))
}

func (c *Compiler) ExprBinaryPlus(n *ast.ExprBinaryPlus) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpAdd))
}

func (c *Compiler) ExprBinaryBitwiseAnd(n *ast.ExprBinaryBitwiseAnd) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpBwAnd))
}

func (c *Compiler) ExprBinaryBitwiseOr(n *ast.ExprBinaryBitwiseOr) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpBwOr))
}

func (c *Compiler) ExprBinaryBitwiseXor(n *ast.ExprBinaryBitwiseXor) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpBwXor))
}

func (c *Compiler) ExprBitwiseNot(n *ast.ExprBitwiseNot) {
	n.Expr.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpBwNot))
}

func (c *Compiler) ExprBinaryBooleanAnd(n *ast.ExprBinaryBooleanAnd) {
	panic("not implemented")
}

func (c *Compiler) ExprBinaryBooleanOr(n *ast.ExprBinaryBooleanOr) {
	panic("not implemented")
}

func (c *Compiler) ExprBinaryCoalesce(n *ast.ExprBinaryCoalesce) {
	panic("not implemented")
}

func (c *Compiler) ExprBinaryConcat(n *ast.ExprBinaryConcat) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpConcat))
}

func (c *Compiler) ExprBinaryDiv(n *ast.ExprBinaryDiv) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpDiv))
}

func (c *Compiler) ExprBinaryEqual(n *ast.ExprBinaryEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpEqual))
}

func (c *Compiler) ExprBinaryGreater(n *ast.ExprBinaryGreater) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpGreater))
}

func (c *Compiler) ExprBinaryGreaterOrEqual(n *ast.ExprBinaryGreaterOrEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpGreaterOrEqual))
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

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpMod))
}

func (c *Compiler) ExprBinaryMul(n *ast.ExprBinaryMul) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpMul))
}

func (c *Compiler) ExprBinaryNotEqual(n *ast.ExprBinaryNotEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpNotEqual))
}

func (c *Compiler) ExprBinaryNotIdentical(n *ast.ExprBinaryNotIdentical) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpNotIdentical))
}

func (c *Compiler) ExprBinaryPow(n *ast.ExprBinaryPow) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpPow))
}

func (c *Compiler) ExprBinaryShiftLeft(n *ast.ExprBinaryShiftLeft) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpShiftLeft))
}

func (c *Compiler) ExprBinaryShiftRight(n *ast.ExprBinaryShiftRight) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpShiftRight))
}

func (c *Compiler) ExprBinarySmaller(n *ast.ExprBinarySmaller) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpLess))
}

func (c *Compiler) ExprBinarySmallerOrEqual(n *ast.ExprBinarySmallerOrEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpLessOrEqual))
}

func (c *Compiler) ExprBinarySpaceship(n *ast.ExprBinarySpaceship) {
	n.Left.Accept(c)
	n.Right.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCompare))
}

func (c *Compiler) ExprCastArray(n *ast.ExprCastArray) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCast))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.ArrayType))
}

func (c *Compiler) ExprCastBool(n *ast.ExprCastBool) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCast))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.BoolType))
}

func (c *Compiler) ExprCastDouble(n *ast.ExprCastDouble) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCast))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.FloatType))
}

func (c *Compiler) ExprCastInt(n *ast.ExprCastInt) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCast))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.IntType))
}

func (c *Compiler) ExprCastObject(n *ast.ExprCastObject) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCast))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.ObjectType))
}

func (c *Compiler) ExprCastString(n *ast.ExprCastString) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCast))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.StringType))
}

func (c *Compiler) ExprCastUnset(n *ast.ExprCastUnset) {
	n.Expr.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpCast))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.NullType))
}

func (c *Compiler) ExprPropertyFetch(n *ast.ExprPropertyFetch) {
	n.Var.Accept(c)
	n.Prop.Accept(c)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpPropertyGet))
}

func (c *Compiler) ExprStaticPropertyFetch(n *ast.ExprStaticPropertyFetch) { panic("not implemented") }

func (c *Compiler) ExprMethodCall(n *ast.ExprMethodCall) { panic("not implemented") }

func (c *Compiler) ExprStaticCall(n *ast.ExprStaticCall) { panic("not implemented") }

func (c *Compiler) ExprIsset(n *ast.ExprIsset) {
	for _, v := range n.Vars {
		v.Accept(c)
	}

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpIsSet))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(len(n.Vars)))
}

func (c *Compiler) ExprBooleanNot(n *ast.ExprBooleanNot) {
	n.Expr.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpNot))
}

func (c *Compiler) ExprRequire(n *ast.ExprRequire) {
	panic("not implemented")
}

func (c *Compiler) ExprRequireOnce(n *ast.ExprRequireOnce) {
	panic("not implemented")
}

func (c *Compiler) ExprInclude(n *ast.ExprInclude) {
	panic("not implemented")
}

func (c *Compiler) ExprIncludeOnce(n *ast.ExprIncludeOnce) {
	panic("not implemented")
}

func (c *Compiler) ExprBrackets(n *ast.ExprBrackets) {
	n.Expr.Accept(c)
}

func (c *Compiler) ScalarLnumber(n *ast.ScalarLnumber) {
	i, _ := strconv.Atoi(unsafe.String(unsafe.SliceData(n.Value), len(n.Value)))

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpConst))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(c.context.Literal(n, vm.Int(i))))
}

func (c *Compiler) ScalarString(n *ast.ScalarString) {
	if n.Value[0] == n.Value[len(n.Value)-1] {
		switch n.Value[0] {
		case '"', '\'', '`':
			n.Value = n.Value[1 : len(n.Value)-1]
		}
	}
	s := unsafe.String(unsafe.SliceData(n.Value), len(n.Value))

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpConst))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(c.context.Literal(n, vm.String(s))))
}

func (c *Compiler) ScalarDnumber(n *ast.ScalarDnumber) {
	f, _ := strconv.ParseFloat(unsafe.String(unsafe.SliceData(n.Value), len(n.Value)), 64)
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpConst))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(c.context.Literal(n, vm.Float(f))))
}

func (c *Compiler) ScalarEncapsed(n *ast.ScalarEncapsed) {
	for _, part := range n.Parts {
		part.Accept(c)
	}
}

func (c *Compiler) ScalarEncapsedStringPart(n *ast.ScalarEncapsedStringPart) {
	s := unsafe.String(unsafe.SliceData(n.Value), len(n.Value))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpConst))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(c.context.Literal(n, vm.String(s))))
	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpConcat))
}

func (c *Compiler) ScalarEncapsedStringVar(n *ast.ScalarEncapsedStringVar) {
	panic("not implemented")
}

func (c *Compiler) ScalarEncapsedStringBrackets(n *ast.ScalarEncapsedStringBrackets) {
	n.Var.Accept(c)

	*c.context.Bytecode() = binary.NativeEndian.AppendUint64(*c.context.Bytecode(), uint64(vm.OpConcat))
}

func NewCompiler(extensions *Extensions) *Compiler {
	if extensions == nil {
		return new(Compiler)
	}

	return &Compiler{extensions: extensions.Exts}
}

func (c *Compiler) Reset() {
	c.contexts = c.contexts[:0]
	c.global = nil
	c.context = nil
}

func (c *Compiler) Compile(input []byte, ctx *vm.GlobalContext) vm.CompiledFunction {
	c.global = &internal.GlobalContext{
		Names: internal.NewNameResolver(nsresolver.NewNamespaceResolver()),
	}

	if !slices.Contains(c.global.Literals, vm.Value(vm.Bool(true))) {
		c.global.Literals = append(c.global.Literals, vm.Bool(true))
	}

	if !slices.Contains(c.global.Literals, vm.Value(vm.Bool(false))) {
		c.global.Literals = append(c.global.Literals, vm.Bool(false))
	}

	if !slices.Contains(c.global.Literals, vm.Value(vm.Null{})) {
		c.global.Literals = append(c.global.Literals, vm.Null{})
	}

	c.global.NamedConstants = map[string]int{
		"true":  slices.Index(c.global.Literals, vm.Value(vm.Bool(true))),
		"false": slices.Index(c.global.Literals, vm.Value(vm.Bool(false))),
		"null":  slices.Index(c.global.Literals, vm.Value(vm.Null{})),
	}
	c.global.Labels = make(map[string]uint64)
	c.arrayWriteMode = make(map[ast.Vertex]bool)
	c.context = c.global
	c.ctx = ctx

	for _, ext := range c.extensions {
		for n, constant := range ext.Constants {
			if !slices.Contains(c.global.Literals, constant) {
				c.global.Literals = append(c.global.Literals, constant)
			}
			c.global.NamedConstants[n] = slices.Index(c.global.Literals, constant)
		}

		for n, fn := range ext.Functions {
			ctx.Functions = append(ctx.Functions, fn)
			c.global.Functions = append(c.global.Functions, n)

			var args []internal.Arg

			for _, arg := range fn.(interface{ GetArgs() []vm.Arg }).GetArgs() {
				args = append(args, internal.Arg{
					Name:  arg.Name,
					IsRef: arg.ByRef,
					Type:  arg.Type.String(),
				})
			}

			c.contexts = append(c.contexts, &internal.FunctionContext{
				Name:    n,
				Args:    args,
				BuiltIn: true,
			})
		}
	}

	node, err := parser.Parse(input, conf.Config{Version: &version.Version{Major: 7, Minor: 4}})

	if err != nil {
		panic(err)
	}

	node.Accept(c)

	ctx.Constants = c.global.Literals
	ctx.Functions = slices.Grow(ctx.Functions, len(c.contexts)+len(c.global.Functions))
	ctx.Functions = ctx.Functions[:len(c.contexts)+len(c.global.Functions)]

	for _, context := range c.contexts {
		if context.BuiltIn {
			continue
		}
		ctx.Functions[slices.Index(c.global.Functions, context.Name)] = vm.CompiledFunction{
			Instructions: Optimizer(context.Instructions),
			Args:         len(context.Args),
			Vars:         len(context.Variables),
		}
	}

	return vm.CompiledFunction{
		Instructions: Optimizer(c.global.Instructions),
		Vars:         len(c.global.Variables),
	}
}

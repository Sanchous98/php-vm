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

const FunctionAliasType = "function"

type Compiler struct {
	visitor.Null

	extensions []Extension

	contexts []*internal.FunctionContext
	global   *internal.GlobalContext
	context  internal.Context
}

func (c *Compiler) Root(n *ast.Root) {
	for _, stmt := range n.Stmts {
		stmt.Accept(c)

		if _, ok := stmt.(*ast.StmtReturn); ok {
			break
		}
	}

	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpReturn))
	})
}

func (c *Compiler) Parameter(n *ast.Parameter) {
	name := c.context.Resolve(n.Var, "variable")
	c.context.Arg(name, n.Type, n.AmpersandTkn != nil)
}

func (c *Compiler) Argument(n *ast.Argument) {
	n.Expr.Accept(c)
}

func (c *Compiler) StmtConstant(n *ast.StmtConstant) {
	n.Expr.Accept(c)

	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		if (*bytecode)[len(*bytecode)-2] == byte(vm.OpConst) {
			*bytecode = (*bytecode)[:len(*bytecode)-2]
		}
	})

	name := c.context.Resolve(n.Name, "const")
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

	for _, param := range n.Params {
		param.Accept(c)
	}

	for _, stmt := range n.Stmts {
		stmt.Accept(c)
	}

	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		switch binary.NativeEndian.Uint64((*bytecode)[len(*bytecode)-8:]) {
		case uint64(vm.OpReturn), uint64(vm.OpReturnValue):
		default:
			*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpReturn))
		}
	})

	if n.ReturnType != nil {
		n.ReturnType.Accept(c)
	}

	c.context = c.context.Parent()
}

func (c *Compiler) StmtIf(n *ast.StmtIf) {
	n.Cond.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		pos := len(*bytecode)
		n.Stmt.Accept(c)

		goTo := (len(*bytecode) + 16) >> 3

		end := make([]byte, len((*bytecode)[pos:]))
		copy(end, (*bytecode)[pos:])

		*bytecode = binary.NativeEndian.AppendUint64((*bytecode)[:pos], uint64(vm.OpJumpFalse))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(goTo))
		*bytecode = append(*bytecode, end...)
	})
}

func (c *Compiler) StmtNop(*ast.StmtNop) {
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpNoop))
	})
}

func (c *Compiler) StmtReturn(n *ast.StmtReturn) {
	if n.Expr == nil {
		c.context.Bytecode(func(bytecode *vm.Bytecode) {
			*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpReturn))
		})
	} else {
		n.Expr.Accept(c)
		c.context.Bytecode(func(bytecode *vm.Bytecode) {
			*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpReturnValue))
		})
	}
}

func (c *Compiler) StmtStmtList(n *ast.StmtStmtList) {
	for _, stmt := range n.Stmts {
		stmt.Accept(c)
	}
}

func (c *Compiler) StmtExpression(n *ast.StmtExpression) {
	n.Expr.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpPop))
	})
}

func (c *Compiler) StmtFor(n *ast.StmtFor) {
	for _, expr := range n.Init {
		expr.Accept(c)
	}

	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpPop))
		condPos := len(*bytecode) >> 3

		for _, cond := range n.Cond {
			cond.Accept(c)
		}

		pos := len(*bytecode)
		n.Stmt.Accept(c)

		for _, loop := range n.Loop {
			loop.Accept(c)
		}

		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpPop))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpJump))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(condPos))

		goTo := (len(*bytecode) + 16) >> 3

		end := make([]byte, len((*bytecode)[pos:]))
		copy(end, (*bytecode)[pos:])

		*bytecode = binary.NativeEndian.AppendUint64((*bytecode)[:pos], uint64(vm.OpJumpFalse))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(goTo))
		*bytecode = append(*bytecode, end...)
	})
}

func (c *Compiler) StmtForeach(n *ast.StmtForeach) {
	// do nothing
}

func (c *Compiler) StmtWhile(n *ast.StmtWhile) {
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		cond := len(*bytecode) >> 3
		n.Cond.Accept(c)
		pos := len(*bytecode)
		n.Stmt.Accept(c)

		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpJump))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(cond))

		goTo := (len(*bytecode) + 16) >> 3

		end := make([]byte, len((*bytecode)[pos:]))
		copy(end, (*bytecode)[pos:])

		*bytecode = binary.NativeEndian.AppendUint64((*bytecode)[:pos], uint64(vm.OpJumpFalse))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(goTo))
		*bytecode = append(*bytecode, end...)
	})
}

func (c *Compiler) StmtDo(n *ast.StmtDo) {
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		pos := len(*bytecode) >> 3
		n.Stmt.Accept(c)
		n.Cond.Accept(c)

		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpJumpFalse))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(pos))
	})
}

func (c *Compiler) ExprFunctionCall(n *ast.ExprFunctionCall) {
	name := c.context.Resolve(n.Function, FunctionAliasType)
	f := slices.IndexFunc(c.contexts, func(context *internal.FunctionContext) bool {
		return context.Name == name
	})

	for i, arg := range n.Args {
		arg.Accept(c)

		if c.contexts[f].Args[i].IsRef {
			c.context.Bytecode(func(bytecode *vm.Bytecode) {
				binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpLoadRef))
			})
		}

		if c.contexts[f].Args[i].Type != nil {
			_type := c.global.Resolve(c.contexts[f].Args[i].Type, "")
			if aT, ok := builtInTypeAsserts[_type]; ok {
				c.context.Bytecode(func(bytecode *vm.Bytecode) {
					*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpAssertType))
					*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(aT))
				})
			}
		}
	}

	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpCall))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(c.context.Function(name)))
	})
}

func (c *Compiler) ExprVariable(n *ast.ExprVariable) {
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		name := c.context.Resolve(n.Name, "variable")
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpLoad))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(c.context.Var(name)))
	})
}

func (c *Compiler) ExprConstFetch(n *ast.ExprConstFetch) {
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		name := c.context.Resolve(n.Const, "const")
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpConst))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(c.context.Constant(name)))
	})
}

func (c *Compiler) ExprAssign(n *ast.ExprAssign) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssign))
	})
}

func (c *Compiler) ExprAssignBitwiseAnd(n *ast.ExprAssignBitwiseAnd) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignBwAnd))
	})
}

func (c *Compiler) ExprAssignBitwiseOr(n *ast.ExprAssignBitwiseOr) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignBwOr))
	})
}

func (c *Compiler) ExprAssignBitwiseXor(n *ast.ExprAssignBitwiseXor) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignBwXor))
	})
}

func (c *Compiler) ExprAssignCoalesce(*ast.ExprAssignCoalesce) {
	panic("not implemented")
}

func (c *Compiler) ExprAssignConcat(n *ast.ExprAssignConcat) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignConcat))
	})
}

func (c *Compiler) ExprAssignPow(n *ast.ExprAssignPow) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignPow))
	})
}

func (c *Compiler) ExprAssignShiftLeft(n *ast.ExprAssignShiftLeft) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignShiftLeft))
	})
}

func (c *Compiler) ExprAssignShiftRight(n *ast.ExprAssignShiftRight) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignShiftRight))
	})
}

func (c *Compiler) ExprAssignReference(n *ast.ExprAssignReference) {
	n.Expr.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpLoadRef))
	})

	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssign))
	})
}

func (c *Compiler) ExprArray(n *ast.ExprArray) {}

func (c *Compiler) ExprArrayDimFetch(n *ast.ExprArrayDimFetch) {}

func (c *Compiler) ExprArrayItem(n *ast.ExprArrayItem) {}

func (c *Compiler) ExprPostInc(n *ast.ExprPostInc) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpPostIncrement))
	})
}

func (c *Compiler) ExprPreInc(n *ast.ExprPreInc) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpPreIncrement))
	})
}

func (c *Compiler) ExprPostDec(n *ast.ExprPostDec) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpPostDecrement))
	})
}

func (c *Compiler) ExprPreDec(n *ast.ExprPreDec) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpPreDecrement))
	})
}

func (c *Compiler) ExprAssignDiv(n *ast.ExprAssignDiv) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignDiv))
	})
}

func (c *Compiler) ExprAssignMinus(n *ast.ExprAssignMinus) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignSub))
	})
}

func (c *Compiler) ExprAssignMod(n *ast.ExprAssignMod) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignMod))
	})
}

func (c *Compiler) ExprAssignMul(n *ast.ExprAssignMul) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignMul))
	})
}

func (c *Compiler) ExprAssignPlus(n *ast.ExprAssignPlus) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		binary.NativeEndian.PutUint64((*bytecode)[len(*bytecode)-16:], uint64(vm.OpAssignAdd))
	})
}

func (c *Compiler) ExprBinaryIdentical(n *ast.ExprBinaryIdentical) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpIdentical))
	})
}

func (c *Compiler) ExprBinaryMinus(n *ast.ExprBinaryMinus) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpSub))
	})
}

func (c *Compiler) ExprBinaryPlus(n *ast.ExprBinaryPlus) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpAdd))
	})
}

func (c *Compiler) ExprBinaryBitwiseAnd(n *ast.ExprBinaryBitwiseAnd) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpBwAnd))
	})
}

func (c *Compiler) ExprBinaryBitwiseOr(n *ast.ExprBinaryBitwiseOr) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpBwOr))
	})
}

func (c *Compiler) ExprBinaryBitwiseXor(n *ast.ExprBinaryBitwiseXor) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpBwXor))
	})
}

func (c *Compiler) ExprBitwiseNot(n *ast.ExprBitwiseNot) {
	n.Expr.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpBwNot))
	})
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
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpConcat))
	})
}

func (c *Compiler) ExprBinaryDiv(n *ast.ExprBinaryDiv) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpDiv))
	})
}

func (c *Compiler) ExprBinaryEqual(n *ast.ExprBinaryEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpEqual))
	})
}

func (c *Compiler) ExprBinaryGreater(n *ast.ExprBinaryGreater) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpGreater))
	})
}

func (c *Compiler) ExprBinaryGreaterOrEqual(n *ast.ExprBinaryGreaterOrEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpGreaterOrEqual))
	})
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
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpMod))
	})
}

func (c *Compiler) ExprBinaryMul(n *ast.ExprBinaryMul) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpMul))
	})
}

func (c *Compiler) ExprBinaryNotEqual(n *ast.ExprBinaryNotEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpNotEqual))
	})
}

func (c *Compiler) ExprBinaryNotIdentical(n *ast.ExprBinaryNotIdentical) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpNotIdentical))
	})
}

func (c *Compiler) ExprBinaryPow(n *ast.ExprBinaryPow) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpPow))
	})
}

func (c *Compiler) ExprBinaryShiftLeft(n *ast.ExprBinaryShiftLeft) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpShiftLeft))
	})
}

func (c *Compiler) ExprBinaryShiftRight(n *ast.ExprBinaryShiftRight) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpShiftRight))
	})
}

func (c *Compiler) ExprBinarySmaller(n *ast.ExprBinarySmaller) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpLess))
	})
}

func (c *Compiler) ExprBinarySmallerOrEqual(n *ast.ExprBinarySmallerOrEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpLessOrEqual))
	})
}

func (c *Compiler) ExprBinarySpaceship(n *ast.ExprBinarySpaceship) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpCompare))
	})
}

func (c *Compiler) ScalarLnumber(n *ast.ScalarLnumber) {
	i, _ := strconv.Atoi(*(*string)(unsafe.Pointer(&n.Value)))
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpConst))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(c.context.Literal(n, vm.Int(i))))
	})
}

func (c *Compiler) ScalarString(n *ast.ScalarString) {
	s, _ := strconv.Unquote(*(*string)(unsafe.Pointer(&n.Value)))
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpConst))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(c.context.Literal(n, vm.String(s))))
	})
}

func (c *Compiler) ScalarDnumber(n *ast.ScalarDnumber) {
	f, _ := strconv.ParseFloat(*(*string)(unsafe.Pointer(&n.Value)), 64)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpConst))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(c.context.Literal(n, vm.Float(f))))
	})
}

func (c *Compiler) StmtEcho(n *ast.StmtEcho) {
	for _, expr := range n.Exprs {
		expr.Accept(c)
	}

	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(vm.OpEcho))
		*bytecode = binary.NativeEndian.AppendUint64(*bytecode, uint64(len(n.Exprs)))
	})
}

func NewCompiler(extensions *Extensions) *Compiler {
	if extensions == nil {
		return new(Compiler)
	}

	return &Compiler{extensions: extensions.exts}
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
	c.context = c.global

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
		}
	}

	node, _ := parser.Parse(input, conf.Config{Version: &version.Version{Major: 7, Minor: 0}})
	node.Accept(c)

	ctx.Constants = c.global.Literals
	ctx.Functions = slices.Grow(ctx.Functions, len(c.contexts)+len(c.global.Functions))
	ctx.Functions = ctx.Functions[:len(c.contexts)+len(c.global.Functions)]

	for _, context := range c.contexts {
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

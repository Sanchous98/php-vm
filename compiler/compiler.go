package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/nsresolver"
	"php-vm/vm"
	"slices"
	"strconv"
	"unsafe"
)

const FunctionAliasType = "function"

type Compiler struct {
	visitor.Null

	contexts []*FunctionContext
	global   *GlobalContext
	context  Context
}

func (c *Compiler) Root(n *ast.Root) {
	for _, stmt := range n.Stmts {
		stmt.Accept(c)
	}
}

func (c *Compiler) Parameter(n *ast.Parameter) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = (*bytecode)[:len(*bytecode)-2]
	})
}

func (c *Compiler) Argument(n *ast.Argument) {
	n.Expr.Accept(c)
}

func (c *Compiler) StmtFunction(n *ast.StmtFunction) {
	ctx := c.context.Child(c.context.Resolve(n.Name, FunctionAliasType))
	c.context = ctx
	c.contexts = append(c.contexts, ctx)

	for _, param := range n.Params {
		param.Accept(c)
	}

	c.context.Args(len(n.Params))

	for _, stmt := range n.Stmts {
		stmt.Accept(c)
	}

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
		*bytecode = slices.Insert(*bytecode, pos, byte(vm.OpJumpNZ), byte(len(*bytecode)+2))
	})
}

func (c *Compiler) StmtNop(*ast.StmtNop) {
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpNoop))
	})
}

func (c *Compiler) StmtReturn(n *ast.StmtReturn) {
	if n.Expr == nil {
		c.context.Bytecode(func(bytecode *vm.Bytecode) {
			*bytecode = append(*bytecode, byte(vm.OpReturn))
		})
	} else {
		n.Expr.Accept(c)
		c.context.Bytecode(func(bytecode *vm.Bytecode) {
			*bytecode = append(*bytecode, byte(vm.OpReturnValue))
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
}

func (c *Compiler) ExprFunctionCall(n *ast.ExprFunctionCall) {
	for _, arg := range n.Args {
		arg.Accept(c)
	}

	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		name := c.context.Resolve(n.Function, FunctionAliasType)
		*bytecode = append(*bytecode, byte(vm.OpCall), byte(c.context.Function(name)))
	})
}

func (c *Compiler) ExprVariable(n *ast.ExprVariable) {
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		name := c.context.Resolve(n.Name, "variable")
		*bytecode = append(*bytecode, byte(vm.OpLoad), byte(c.context.Var(name)))
	})
}

func (c *Compiler) ExprConstFetch(n *ast.ExprConstFetch) {
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		name := c.context.Resolve(n.Const, "const")
		*bytecode = append(*bytecode, byte(vm.OpConst), byte(c.context.Constant(name)))
	})
}

func (c *Compiler) ExprAssign(n *ast.ExprAssign) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssign)
	})
}

func (c *Compiler) ExprAssignBitwiseAnd(n *ast.ExprAssignBitwiseAnd) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignBwAnd)
	})
}

func (c *Compiler) ExprAssignBitwiseOr(n *ast.ExprAssignBitwiseOr) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignBwOr)
	})
}

func (c *Compiler) ExprAssignBitwiseXor(n *ast.ExprAssignBitwiseXor) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignBwXor)
	})
}

func (c *Compiler) ExprAssignCoalesce(n *ast.ExprAssignCoalesce) {
	panic("not implemented")
}

func (c *Compiler) ExprAssignConcat(n *ast.ExprAssignConcat) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignConcat)
	})
}

func (c *Compiler) ExprAssignPow(n *ast.ExprAssignPow) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignPow)
	})
}

func (c *Compiler) ExprAssignShiftLeft(n *ast.ExprAssignShiftLeft) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignShiftLeft)
	})
}

func (c *Compiler) ExprAssignShiftRight(n *ast.ExprAssignShiftRight) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignShiftRight)
	})
}

func (c *Compiler) ExprPostInc(n *ast.ExprPostInc) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpPostIncrement)
	})
}

func (c *Compiler) ExprPreInc(n *ast.ExprPreInc) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpPreIncrement)
	})
}

func (c *Compiler) ExprPostDec(n *ast.ExprPostDec) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpPostDecrement)
	})
}

func (c *Compiler) ExprPreDec(n *ast.ExprPreDec) {
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpPreDecrement)
	})
}

func (c *Compiler) ExprAssignDiv(n *ast.ExprAssignDiv) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignDiv)
	})
}

func (c *Compiler) ExprAssignMinus(n *ast.ExprAssignMinus) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignSub)
	})
}

func (c *Compiler) ExprAssignMod(n *ast.ExprAssignMod) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignMod)
	})
}

func (c *Compiler) ExprAssignMul(n *ast.ExprAssignMul) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignMul)
	})
}

func (c *Compiler) ExprAssignPlus(n *ast.ExprAssignPlus) {
	n.Expr.Accept(c)
	n.Var.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		(*bytecode)[len(*bytecode)-2] = byte(vm.OpAssignAdd)
	})
}

func (c *Compiler) ExprBinaryIdentical(n *ast.ExprBinaryIdentical) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpIdentical))
	})
}

func (c *Compiler) ExprBinaryMinus(n *ast.ExprBinaryMinus) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpSub))
	})
}

func (c *Compiler) ExprBinaryPlus(n *ast.ExprBinaryPlus) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpAdd))
	})
}

func (c *Compiler) ExprBinaryBitwiseAnd(n *ast.ExprBinaryBitwiseAnd) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpBwAnd))
	})
}

func (c *Compiler) ExprBinaryBitwiseOr(n *ast.ExprBinaryBitwiseOr) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpBwOr))
	})
}

func (c *Compiler) ExprBinaryBitwiseXor(n *ast.ExprBinaryBitwiseXor) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpBwXor))
	})
}

func (c *Compiler) ExprBitwiseNot(n *ast.ExprBitwiseNot) {
	n.Expr.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpBwNot))
	})
}

func (c *Compiler) ExprBinaryBooleanAnd(n *ast.ExprBinaryBooleanAnd) {
	// do nothing
}

func (c *Compiler) ExprBinaryBooleanOr(n *ast.ExprBinaryBooleanOr) {
	// do nothing
}

func (c *Compiler) ExprBinaryCoalesce(n *ast.ExprBinaryCoalesce) {
	// do nothing
}

func (c *Compiler) ExprBinaryConcat(n *ast.ExprBinaryConcat) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpConcat))
	})
}

func (c *Compiler) ExprBinaryDiv(n *ast.ExprBinaryDiv) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpDiv))
	})
}

func (c *Compiler) ExprBinaryEqual(n *ast.ExprBinaryEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpEqual))
	})
}

func (c *Compiler) ExprBinaryGreater(n *ast.ExprBinaryGreater) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpGreater))
	})
}

func (c *Compiler) ExprBinaryGreaterOrEqual(n *ast.ExprBinaryGreaterOrEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpGreaterOrEqual))
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
		*bytecode = append(*bytecode, byte(vm.OpMod))
	})
}

func (c *Compiler) ExprBinaryMul(n *ast.ExprBinaryMul) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpMul))
	})
}

func (c *Compiler) ExprBinaryNotEqual(n *ast.ExprBinaryNotEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpNotEqual))
	})
}

func (c *Compiler) ExprBinaryNotIdentical(n *ast.ExprBinaryNotIdentical) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpNotIdentical))
	})
}

func (c *Compiler) ExprBinaryPow(n *ast.ExprBinaryPow) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpPow))
	})
}

func (c *Compiler) ExprBinaryShiftLeft(n *ast.ExprBinaryShiftLeft) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpShiftLeft))
	})
}

func (c *Compiler) ExprBinaryShiftRight(n *ast.ExprBinaryShiftRight) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpShiftRight))
	})
}

func (c *Compiler) ExprBinarySmaller(n *ast.ExprBinarySmaller) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpLess))
	})
}

func (c *Compiler) ExprBinarySmallerOrEqual(n *ast.ExprBinarySmallerOrEqual) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpLessOrEqual))
	})
}

func (c *Compiler) ExprBinarySpaceship(n *ast.ExprBinarySpaceship) {
	n.Left.Accept(c)
	n.Right.Accept(c)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpCompare))
	})
}

func (c *Compiler) ScalarLnumber(n *ast.ScalarLnumber) {
	i, _ := strconv.Atoi(*(*string)(unsafe.Pointer(&n.Value)))
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpConst), byte(c.context.Literal(vm.Int(i))))
	})
}

func (c *Compiler) ScalarString(n *ast.ScalarString) {
	s := *(*string)(unsafe.Pointer(&n.Value))
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpConst), byte(c.context.Literal(vm.String(s))))
	})
}

func (c *Compiler) ScalarDnumber(n *ast.ScalarDnumber) {
	f, _ := strconv.ParseFloat(*(*string)(unsafe.Pointer(&n.Value)), 64)
	c.context.Bytecode(func(bytecode *vm.Bytecode) {
		*bytecode = append(*bytecode, byte(vm.OpConst), byte(c.context.Literal(vm.Float(f))))
	})
}

func (c *Compiler) Compile(input []byte) (vm.CompiledFunction, *vm.GlobalContext) {
	c.global = &GlobalContext{
		names:          NewNameResolver(nsresolver.NewNamespaceResolver()),
		constants:      []vm.Value{vm.Bool(true), vm.Bool(false)},
		namedConstants: map[string]int{"true": 0, "false": 1},
	}
	c.context = c.global
	node, _ := parser.Parse(input, conf.Config{Version: &version.Version{Major: 7, Minor: 2}})
	node.Accept(c)

	global := new(vm.GlobalContext)
	global.Constants = c.global.constants
	global.Functions = make([]vm.Callable, len(c.contexts))

	for pointer, context := range c.contexts {
		global.Functions[pointer] = vm.CompiledFunction{
			Instructions: context.bytecode,
			Args:         context.ArgsNum,
			Vars:         len(context.variables),
		}
	}

	return vm.CompiledFunction{
		Instructions: c.global.bytecode,
		Vars:         len(c.global.variables),
	}, global
}

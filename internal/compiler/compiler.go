package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"
	"github.com/VKCOM/php-parser/pkg/visitor"
	"github.com/VKCOM/php-parser/pkg/visitor/nsresolver"
	"php-vm/internal/compiler/internal"
	"php-vm/internal/vm"
	"slices"
	"strconv"
	"strings"
	"unsafe"
)

var posixReplacer = strings.NewReplacer("\\a", "\a", "\\b", "\b", "\\n", "\n", "\\r", "\r", "\\t", "\t", "\\v", "\v", "\\f", "\f")

func init() { posixReplacer.Replace("") }

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

	if len(*c.context.Bytecode()) == 0 {
		return
	}

	switch (*c.context.Bytecode())[len(*c.context.Bytecode())-1] >> 32 {
	case uint64(vm.OpReturn), uint64(vm.OpReturnValue):
	default:
		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpReturn)<<32)
	}
}

func (c *Compiler) Parameter(n *ast.Parameter) {
	name := c.context.Resolve(n.Var, VariableAliasType)
	_type := c.context.Resolve(n.Type, "")
	if n.DefaultValue != nil {
		n.DefaultValue.Accept(c)
	}
	c.context.Arg(name, _type, n.DefaultValue, n.AmpersandTkn != nil, n.VariadicTkn != nil)
}

func (c *Compiler) Argument(n *ast.Argument) {
	n.Expr.Accept(c)
}

func (c *Compiler) ExprFunctionCall(n *ast.ExprFunctionCall) {
	name := c.context.Resolve(n.Function, FunctionAliasType)
	f := slices.IndexFunc(c.contexts, func(ctx *internal.FunctionContext) bool { return ctx.Name == name })

	if f < 0 {
		return
	}

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpInitCall)<<32+uint64(f))

	for index, arg := range n.Args {
		arg.Accept(c)

		switch arg.(*ast.Argument).Expr.(type) {
		case *ast.ExprVariable:
			if c.contexts[f].Args[index].IsRef {
				c.assignOp(vm.OpLoadRef)
			}
		}
	}

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpCall)<<32+uint64(len(n.Args)))
}

func (c *Compiler) ExprVariable(n *ast.ExprVariable) {
	name := c.context.Resolve(n.Name, VariableAliasType)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpLoad)<<32+uint64(c.context.Var(name)))
}

func (c *Compiler) ExprConstFetch(n *ast.ExprConstFetch) {
	name := c.context.Resolve(n.Const, ConstantAliasType)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Constant(name)))
}

func (c *Compiler) ExprArray(n *ast.ExprArray) {
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpArrayNew)<<32)

	for _, item := range n.Items {
		item.Accept(c)
	}
}

func (c *Compiler) ExprArrayDimFetch(n *ast.ExprArrayDimFetch) {
	if n.Dim == nil {
		c.arrayWriteMode[n.Var] = true
		n.Var.Accept(c)
		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpArrayAccessPush)<<32)
	} else {
		if c.arrayWriteMode[n] {
			switch n.Var.(type) {
			case *ast.ExprArrayDimFetch:
				c.arrayWriteMode[n.Var] = true
			}
			n.Var.Accept(c)
			n.Dim.Accept(c)
			*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpArrayAccessWrite)<<32)
		} else {
			n.Var.Accept(c)
			n.Dim.Accept(c)
			*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpArrayAccessRead)<<32)
		}
	}
}

func (c *Compiler) ExprArrayItem(n *ast.ExprArrayItem) {
	if n.Key == nil {
		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpArrayAccessPush)<<32)
	} else {
		n.Key.Accept(c)
		*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpArrayAccessWrite)<<32)
	}
	n.Val.Accept(c)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpAssignRef)<<32, uint64(vm.OpPop)<<32)
}

func (c *Compiler) ExprPropertyFetch(*ast.ExprPropertyFetch) {
	panic("not implemented")
}

func (c *Compiler) ExprStaticPropertyFetch(*ast.ExprStaticPropertyFetch) { panic("not implemented") }

func (c *Compiler) ExprMethodCall(*ast.ExprMethodCall) { panic("not implemented") }

func (c *Compiler) ExprStaticCall(*ast.ExprStaticCall) { panic("not implemented") }

func (c *Compiler) ExprIsset(n *ast.ExprIsset) {
	for _, v := range n.Vars {
		v.Accept(c)
	}

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpIsSet)<<32+uint64(len(n.Vars)))
}

func (c *Compiler) ExprRequire(*ast.ExprRequire) {
	panic("not implemented")
}

func (c *Compiler) ExprRequireOnce(*ast.ExprRequireOnce) {
	panic("not implemented")
}

func (c *Compiler) ExprInclude(*ast.ExprInclude) {
	panic("not implemented")
}

func (c *Compiler) ExprIncludeOnce(*ast.ExprIncludeOnce) {
	panic("not implemented")
}

func (c *Compiler) ExprBrackets(n *ast.ExprBrackets) {
	n.Expr.Accept(c)
}

func (c *Compiler) ScalarLnumber(n *ast.ScalarLnumber) {
	i, _ := strconv.Atoi(unsafe.String(unsafe.SliceData(n.Value), len(n.Value)))

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Literal(n, vm.Int(i))))
}

func (c *Compiler) ScalarString(n *ast.ScalarString) {
	if n.Value[0] == n.Value[len(n.Value)-1] {
		switch n.Value[0] {
		case '"', '\'', '`':
			n.Value = n.Value[1 : len(n.Value)-1]
		}
	}

	s := posixReplacer.Replace(unsafe.String(unsafe.SliceData(n.Value), len(n.Value)))

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Literal(n, vm.String(s))))
	*c.context.Bytecode() = append(*c.context.Bytecode())
}

func (c *Compiler) ScalarDnumber(n *ast.ScalarDnumber) {
	f, _ := strconv.ParseFloat(unsafe.String(unsafe.SliceData(n.Value), len(n.Value)), 64)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Literal(n, vm.Float(f))))
}

func (c *Compiler) ScalarEncapsed(n *ast.ScalarEncapsed) {
	for _, part := range n.Parts {
		part.Accept(c)
	}
}

func (c *Compiler) ScalarEncapsedStringPart(n *ast.ScalarEncapsedStringPart) {
	s := unsafe.String(unsafe.SliceData(n.Value), len(n.Value))
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Literal(n, vm.String(s))))
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConcat)<<32)
}

func (c *Compiler) ScalarEncapsedStringVar(n *ast.ScalarEncapsedStringVar) {
	panic("not implemented")
}

func (c *Compiler) ScalarEncapsedStringBrackets(n *ast.ScalarEncapsedStringBrackets) {
	n.Var.Accept(c)

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConcat)<<32)
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

func (c *Compiler) Compile(input []byte, ctx *vm.GlobalContext) vm.Function {
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
			fn := fn
			ctx.Functions = append(ctx.Functions, &fn)
			c.global.Functions = append(c.global.Functions, n)

			var args []internal.Arg

			c.contexts = append(c.contexts, &internal.FunctionContext{
				Name:    n,
				Args:    args,
				BuiltIn: true,
			})
		}
	}

	var parseErrors []*errors.Error

	node, err := parser.Parse(input, conf.Config{
		Version:          &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) { parseErrors = append(parseErrors, e) },
	})

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

		ctx.Functions[slices.Index(c.global.Functions, context.Name)] = &vm.Function{
			FuncName:   vm.String(context.Name),
			Executable: Optimizer(context.Instructions),
			Vars:       *(*[]vm.String)(unsafe.Pointer(&context.Variables)),
		}
	}

	return vm.Function{
		Executable: Optimizer(c.global.Instructions),
		Vars:       *(*[]vm.String)(unsafe.Pointer(&c.global.Variables)),
	}
}

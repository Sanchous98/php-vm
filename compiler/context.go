package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/vm"
	"slices"
)

type FunctionContext struct {
	Context

	Name      string
	Names     *NameResolver
	ArgsNum   int
	bytecode  vm.Bytecode
	variables []string
}

func (ctx *FunctionContext) Parent() Context { return ctx.Context }
func (ctx *FunctionContext) Child(fn string) *FunctionContext {
	return &FunctionContext{
		Context: ctx,
		Name:    fn,
		Names:   NewNameResolver(ctx.Global().names.NamespaceResolver),
	}
}
func (ctx *FunctionContext) Global() *GlobalContext { return ctx.Context.Global() }
func (ctx *FunctionContext) Bytecode(fn func(bytecode *vm.Bytecode)) {
	fn(&ctx.bytecode)
}
func (ctx *FunctionContext) Args(i int) { ctx.ArgsNum = i }
func (ctx *FunctionContext) Resolve(vertex ast.Vertex, aliasType string) string {
	ctx.Names.Resolve(vertex, aliasType)

	if aliasType == "variable" {
		if !slices.Contains(ctx.variables, ctx.Names.Variables[vertex]) {
			ctx.variables = append(ctx.variables, ctx.Names.Variables[vertex])
		}

		return ctx.Names.Variables[vertex]
	}

	if !slices.Contains(ctx.Global().functions, ctx.Global().names.ResolvedNames[vertex]) {
		ctx.Global().functions = append(ctx.Global().functions, ctx.Global().names.ResolvedNames[vertex])
	}

	return ctx.Global().names.ResolvedNames[vertex]
}
func (ctx *FunctionContext) Var(n string) int { return slices.Index(ctx.variables, n) }

type GlobalContext struct {
	names          *NameResolver
	constants      []vm.Value
	namedConstants map[string]int
	bytecode       vm.Bytecode
	variables      []string
	functions      []string
}

func (ctx *GlobalContext) Parent() Context { return nil }
func (ctx *GlobalContext) Child(fn string) *FunctionContext {
	return &FunctionContext{
		Context: ctx,
		Name:    fn,
		Names:   NewNameResolver(ctx.Global().names.NamespaceResolver),
	}
}
func (ctx *GlobalContext) Global() *GlobalContext { return ctx }
func (ctx *GlobalContext) Literal(v vm.Value) int {
	if !slices.Contains(ctx.constants, v) {
		ctx.constants = append(ctx.constants, v)
	}

	return slices.Index(ctx.constants, v)
}
func (ctx *GlobalContext) Bytecode(fn func(bytecode *vm.Bytecode)) {
	fn(&ctx.bytecode)
}
func (ctx *GlobalContext) Args(int) {}
func (ctx *GlobalContext) Resolve(vertex ast.Vertex, aliasType string) string {
	ctx.names.Resolve(vertex, aliasType)

	if aliasType == "variable" {
		if !slices.Contains(ctx.variables, ctx.names.Variables[vertex]) {
			ctx.variables = append(ctx.variables, ctx.names.Variables[vertex])
		}

		return ctx.names.Variables[vertex]
	}

	if !slices.Contains(ctx.functions, ctx.names.ResolvedNames[vertex]) {
		ctx.functions = append(ctx.functions, ctx.names.ResolvedNames[vertex])
	}

	return ctx.names.ResolvedNames[vertex]
}
func (ctx *GlobalContext) Function(fn string) int { return slices.Index(ctx.functions, fn) }
func (ctx *GlobalContext) Constant(c string) int {
	if v, ok := ctx.namedConstants[c]; ok {
		return v
	}

	panic("constant not defined")
}
func (ctx *GlobalContext) Var(n string) int { return slices.Index(ctx.variables, n) }

type Context interface {
	Parent() Context
	Child(string) *FunctionContext
	Global() *GlobalContext
	Bytecode(func(*vm.Bytecode))
	Args(int)
	Literal(vm.Value) int
	Resolve(ast.Vertex, string) string
	Function(string) int
	Constant(string) int
	Var(string) int
}

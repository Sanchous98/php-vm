package internal

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/internal/vm"
	"slices"
)

type Context interface {
	Parent() Context
	Child(string) *FunctionContext
	Global() *GlobalContext
	Bytecode(func(*vm.Bytecode))
	Args(int)
	Literal(ast.Vertex, vm.Value) int
	Resolve(ast.Vertex, string) string
	Function(string) int
	Constant(string) int
	Var(string) int
}

type FunctionContext struct {
	Context

	Name        string
	Names       *NameResolver
	ArgsNum     int
	Instruction vm.Bytecode
	Variables   []string
}

func (ctx *FunctionContext) Parent() Context { return ctx.Context }
func (ctx *FunctionContext) Child(fn string) *FunctionContext {
	return &FunctionContext{
		Context: ctx,
		Name:    fn,
		Names:   NewNameResolver(ctx.Global().Names.NamespaceResolver),
	}
}
func (ctx *FunctionContext) Global() *GlobalContext { return ctx.Context.Global() }
func (ctx *FunctionContext) Bytecode(fn func(bytecode *vm.Bytecode)) {
	fn(&ctx.Instruction)
}
func (ctx *FunctionContext) Args(i int) { ctx.ArgsNum = i }
func (ctx *FunctionContext) Resolve(vertex ast.Vertex, aliasType string) string {
	ctx.Names.Resolve(vertex, aliasType)

	if aliasType == "variable" {
		if !slices.Contains(ctx.Variables, ctx.Names.Variables[vertex]) {
			ctx.Variables = append(ctx.Variables, ctx.Names.Variables[vertex])
		}

		return ctx.Names.Variables[vertex]
	}

	if !slices.Contains(ctx.Global().Functions, ctx.Global().Names.ResolvedNames[vertex]) {
		ctx.Global().Functions = append(ctx.Global().Functions, ctx.Global().Names.ResolvedNames[vertex])
	}

	return ctx.Global().Names.ResolvedNames[vertex]
}
func (ctx *FunctionContext) Var(n string) int { return slices.Index(ctx.Variables, n) }

type GlobalContext struct {
	Names          *NameResolver
	Constants      map[ast.Vertex]int
	Literals       []vm.Value
	NamedConstants map[string]int
	Instructions   vm.Bytecode
	Variables      []string
	Functions      []string
}

func (ctx *GlobalContext) Parent() Context { return nil }
func (ctx *GlobalContext) Child(fn string) *FunctionContext {
	return &FunctionContext{
		Context: ctx,
		Name:    fn,
		Names:   NewNameResolver(ctx.Global().Names.NamespaceResolver),
	}
}
func (ctx *GlobalContext) Global() *GlobalContext { return ctx }
func (ctx *GlobalContext) Literal(n ast.Vertex, v vm.Value) int {
	if ctx.Constants == nil {
		ctx.Constants = make(map[ast.Vertex]int)
	}

	if v, ok := ctx.Constants[n]; ok {
		return v
	}

	if !slices.Contains(ctx.Literals, v) {
		ctx.Literals = append(ctx.Literals, v)
	}

	ctx.Constants[n] = slices.Index(ctx.Literals, v)
	return ctx.Constants[n]
}
func (ctx *GlobalContext) Bytecode(fn func(bytecode *vm.Bytecode)) {
	fn(&ctx.Instructions)
}
func (ctx *GlobalContext) Args(int) {}
func (ctx *GlobalContext) Resolve(vertex ast.Vertex, aliasType string) string {
	ctx.Names.Resolve(vertex, aliasType)

	if aliasType == "variable" {
		if !slices.Contains(ctx.Variables, ctx.Names.Variables[vertex]) {
			ctx.Variables = append(ctx.Variables, ctx.Names.Variables[vertex])
		}

		return ctx.Names.Variables[vertex]
	}

	if !slices.Contains(ctx.Functions, ctx.Names.ResolvedNames[vertex]) {
		ctx.Functions = append(ctx.Functions, ctx.Names.ResolvedNames[vertex])
	}

	return ctx.Names.ResolvedNames[vertex]
}
func (ctx *GlobalContext) Function(fn string) int { return slices.Index(ctx.Functions, fn) }
func (ctx *GlobalContext) Constant(c string) int {
	if v, ok := ctx.NamedConstants[c]; ok {
		return v
	}

	panic("constant not defined")
}
func (ctx *GlobalContext) Var(n string) int { return slices.Index(ctx.Variables, n) }

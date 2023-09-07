package internal

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/internal/vm"
	"slices"
)

type Arg struct {
	Name    string
	Type    string
	Default ast.Vertex
	IsRef   bool
}

type Context interface {
	Arg(string, string, ast.Vertex, bool) Arg
	Parent() Context
	Child(string) *FunctionContext
	Global() *GlobalContext
	Bytecode(func(*vm.Bytecode))
	Literal(ast.Vertex, vm.Value) int
	Resolve(ast.Vertex, string) string
	Function(string) int
	Constant(string) int
	Var(string) int
	AddLabel(string, uint64)
	FindLabel(string) uint64
}

type FunctionContext struct {
	Context

	Name         string
	Names        *NameResolver
	Args         []Arg
	Instructions vm.Bytecode
	Variables    []string
	BuiltIn      bool
	Labels       map[string]uint64
}

func (ctx *FunctionContext) Parent() Context { return ctx.Context }
func (ctx *FunctionContext) Child(fn string) *FunctionContext {
	return &FunctionContext{
		Context: ctx,
		Name:    fn,
		Labels:  map[string]uint64{},
		Names:   NewNameResolver(ctx.Global().Names.NamespaceResolver),
	}
}
func (ctx *FunctionContext) Global() *GlobalContext { return ctx.Context.Global() }
func (ctx *FunctionContext) Bytecode(fn func(bytecode *vm.Bytecode)) {
	fn(&ctx.Instructions)
}
func (ctx *FunctionContext) Arg(name string, _type string, def ast.Vertex, isRef bool) Arg {
	if i := slices.IndexFunc(ctx.Args, func(arg Arg) bool { return arg.Name == name }); i >= 0 {
		return ctx.Args[i]
	}

	a := Arg{name, _type, def, isRef}
	ctx.Args = append(ctx.Args, a)
	return a
}
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
func (ctx *FunctionContext) Var(n string) int                  { return slices.Index(ctx.Variables, n) }
func (ctx *FunctionContext) AddLabel(label string, pos uint64) { ctx.Labels[label] = pos }
func (ctx *FunctionContext) FindLabel(label string) uint64     { return ctx.Labels[label] }

type GlobalContext struct {
	Names          *NameResolver
	Constants      map[ast.Vertex]int
	Literals       []vm.Value
	NamedConstants map[string]int
	Instructions   vm.Bytecode
	Variables      []string
	Functions      []string
	Labels         map[string]uint64
}

func (ctx *GlobalContext) Parent() Context { return nil }
func (ctx *GlobalContext) Child(fn string) *FunctionContext {
	return &FunctionContext{
		Context: ctx,
		Name:    fn,
		Labels:  map[string]uint64{},
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
func (ctx *GlobalContext) Arg(string, string, ast.Vertex, bool) Arg { return Arg{} }
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
func (ctx *GlobalContext) Var(n string) int                  { return slices.Index(ctx.Variables, n) }
func (ctx *GlobalContext) AddLabel(label string, pos uint64) { ctx.Labels[label] = pos }
func (ctx *GlobalContext) FindLabel(label string) uint64     { return ctx.Labels[label] }

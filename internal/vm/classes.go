package vm

import (
	"fmt"
	"strings"
)

type Class interface {
	Name() String
	Get(Context, String) Value
	Set(Context, String, Value)
	IsSet(Context, String) Bool
	UnSet(Context, String)
	ToString(Context, *Object) String
	DebugInfo(Context) String
}

type DefaultHandlers struct{}

func (h DefaultHandlers) Get(ctx Context, name String) Value {
	if v, ok := ctx.This().props.access(name); ok {
		return Ref{v}
	}
	return Null{}
}
func (h DefaultHandlers) Set(ctx Context, name String, v Value) {
	*ctx.This().props.assign(name) = v
}
func (h DefaultHandlers) IsSet(ctx Context, name String) Bool {
	_, ok := ctx.This().props.access(name)
	return Bool(ok)
}
func (h DefaultHandlers) UnSet(ctx Context, name String) { ctx.This().props.delete(name) }
func (h DefaultHandlers) ToString(Context, *Object) String {
	panic("is not convertable to string")
}
func (h DefaultHandlers) DebugInfo(ctx Context) String {
	var str strings.Builder
	str.WriteString(fmt.Sprintf("object(%s)#%d (%d) {", ctx.This().impl.Name() /*TODO: count references*/, 1, len(ctx.This().props.keys)))

	for key, i := range ctx.This().props.keys {
		str.WriteString(stringIndent(fmt.Sprintf("\n[%v]=>\n%s\n", key, ctx.This().props.values[i].DebugInfo(ctx)), 2))
	}

	str.WriteString("\n}")
	return String(str.String())
}

type StdClass struct {
	DefaultHandlers
}

func (c *StdClass) Name() String { return "stdClass" }

type Closure struct {
	DefaultHandlers

	fn    Callable
	this  *Object
	scope Class
}

func (c *Closure) Name() String                                  { return "Closure" }
func (c *Closure) Get(Context, String) Value                     { return Null{} }
func (c *Closure) Set(Context, String, Value)                    { panic("cannot set dynamic properties") }
func (c *Closure) IsSet(Context, String) Bool                    { return false }
func (c *Closure) Invoke(ctx Context, scope Class, this *Object) { c.fn.Invoke(ctx, scope, this) }
func (c *Closure) DebugInfo(parent Context) String {
	props := map[Value]Value{
		String("function"): c.fn.Name(),
		String("this"):     parent.This(),
		// String("parameter"): NewArray(...),
		// TODO: Implement function symbol table
	}

	var ctx FunctionContext
	parent.Child(&ctx, 0, nil, NewObject(c, props))

	return c.DefaultHandlers.DebugInfo(&ctx)
}

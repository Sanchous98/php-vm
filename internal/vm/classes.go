package vm

import (
	"fmt"
	"strings"
)

type PropertyInfo struct {
}

type Class interface {
	Name() String

	// Magic methods
	Construct(Context, *Object)
	Destruct(Context, *Object)
	Clone(Context, *Object)
	Get(Context, *Object, String) Value
	Set(Context, *Object, String, Value)
	IsSet(Context, *Object, String) Bool
	UnSet(Context, *Object, String)
	Invoke(Context, *Object)
	DebugInfo(Context, *Object) String
	ToString(Context, *Object) String
	Call(Context, *Object, String)
	CallStatic(Context, Class, String)

	// Operators
	InstanceOf(Context, Class) Bool
}

type BaseClass struct{}

func (c BaseClass) Name() String                        { panic("not implemented") }
func (c BaseClass) Get(Context, *Object, String) Value  { return nil }
func (c BaseClass) Set(Context, *Object, String, Value) {}
func (c BaseClass) IsSet(Context, *Object, String) Bool { return false }
func (c BaseClass) UnSet(Context, *Object, String)      {}
func (c BaseClass) Invoke(Context, *Object)             { panic("not callable") }
func (c BaseClass) DebugInfo(ctx Context, o *Object) String {
	var str strings.Builder
	str.WriteString(fmt.Sprintf("object(%s)#%d (%d) {", o.class.Name(), 1, len(o.props.internal)))

	for _, key := range o.props.keys(nil) {
		str.WriteString(stringIndent(fmt.Sprintf("\n[%v]=>\n%s\n", key, o.props.internal[key].v.DebugInfo(ctx)), 2))
	}

	str.WriteString("\n}")

	return String(str.String())
}
func (c BaseClass) ToString(Context, *Object) String  { panic("cannot be converted to string") }
func (c BaseClass) Construct(Context, *Object)        {}
func (c BaseClass) Destruct(Context, *Object)         {}
func (c BaseClass) Clone(Context, *Object)            {}
func (c BaseClass) Call(Context, *Object, String)     { panic("method is not declared") }
func (c BaseClass) CallStatic(Context, Class, String) { panic("method is not declared") }
func (c BaseClass) InstanceOf(Context, Class) Bool    { return false }

type StdClass struct{ BaseClass }

func (c *StdClass) InstanceOf(_ Context, class Class) Bool { return c == class }
func (c *StdClass) Get(_ Context, o *Object, name String) Value {
	v, _ := o.props.access(name)
	return NewRef(v)
}
func (c *StdClass) Set(_ Context, o *Object, name String, v Value) { *o.props.assign(name) = v }
func (c *StdClass) IsSet(_ Context, o *Object, name String) Bool {
	_, ok := o.props.access(name)
	return Bool(ok)
}
func (c *StdClass) UnSet(_ Context, o *Object, name String) { o.props.delete(name) }
func (c *StdClass) Name() String                            { return "stdClass" }

type CompiledClass struct {
	BaseClass

	ClassName String

	Constants        map[String]Value
	Methods          map[String]Callable
	StaticProperties map[String]Value

	PropertiesDefinition struct{}
}

func (c *CompiledClass) Name() String { return c.ClassName }
func (c *CompiledClass) Construct(ctx Context, o *Object) {
	if m, ok := c.Methods["__construct"]; ok {
		m.Invoke(ctx, o.class, o)
	}
}

func (c *CompiledClass) Destruct(ctx Context, o *Object) {
	if m, ok := c.Methods["__destruct"]; ok {
		m.Invoke(ctx, o.class, o)
	}
}

func (c *CompiledClass) Clone(ctx Context, o *Object) {
	if m, ok := c.Methods["__clone"]; ok {
		m.Invoke(ctx, o.class, o)
	}
}

func (c *CompiledClass) Get(ctx Context, o *Object, name String) Value {
	// TODO: Check access based on definition

	v, _ := o.props.access(name)
	return NewRef(v)
}

func (c *CompiledClass) Set(ctx Context, o *Object, name String, v Value) {
	// TODO: Check access based on definition

	*o.props.assign(name) = v
}

func (c *CompiledClass) IsSet(ctx Context, o *Object, name String) Bool {
	// TODO: Check access based on definition

	_, ok := o.props.access(name)

	if !ok {
		if m, ok := c.Methods["__isset"]; ok {
			m.Invoke(ctx, o.class, o)
			return ctx.Pop().AsBool(ctx)
		}
		return false
	}

	return true
}

func (c *CompiledClass) UnSet(ctx Context, o *Object, name String) {
	// TODO: Check access based on definition

	o.props.delete(name)
}

func (c *CompiledClass) Invoke(ctx Context, o *Object) {
	if m, ok := c.Methods["__invoke"]; ok {
		m.Invoke(ctx, o.class, o)
		return
	}

	panic(fmt.Sprintf("Object of type %s is not callable", c.Name()))
}

func (c *CompiledClass) DebugInfo(ctx Context, o *Object) String {
	if m, ok := c.Methods["__debugInfo"]; ok {
		m.Invoke(ctx, o.class, o)
		return ctx.Pop().AsString(ctx)
	}

	return c.BaseClass.DebugInfo(ctx, o)
}

func (c *CompiledClass) ToString(ctx Context, o *Object) String {
	if m, ok := c.Methods["__toString"]; ok {
		m.Invoke(ctx, o.class, o)
		return ctx.Pop().AsString(ctx)
	}

	panic(fmt.Sprintf("Object of class %s could not be converted to string", c.Name()))
}

func (c *CompiledClass) Call(ctx Context, o *Object, name String) {
	if m, ok := c.Methods[name]; ok {
		m.Invoke(ctx, o.class, o)
		return
	}

	if m, ok := c.Methods["__call"]; ok {
		m.Invoke(ctx, o.class, o)
		return
	}

	panic(fmt.Sprintf("Call to undefined method %s::%s", c.Name(), name))
}

func (c *CompiledClass) CallStatic(ctx Context, scope Class, name String) {
	if m, ok := c.Methods[name]; ok {
		m.Invoke(ctx, scope, nil)
		return
	}

	if m, ok := c.Methods["__callStatic"]; ok {
		m.Invoke(ctx, scope, nil)
		return
	}

	panic(fmt.Sprintf("Call to undefined method %s::%s", c.Name(), name))
}

func (c *CompiledClass) InstanceOf(ctx Context, class Class) Bool { return c == class }

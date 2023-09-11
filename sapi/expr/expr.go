package expr

import (
	"context"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"reflect"
	"unsafe"
)

type Expression string

func (e Expression) Execute(args map[string]any) vm.Value {
	return e.ExecuteContext(context.Background(), args)
}

func convertValue(v any) vm.Value {
	switch value := v.(type) {
	case int:
		return vm.Int(value)
	case uint:
		return vm.Int(value)
	case int8:
		return vm.Int(value)
	case uint8:
		return vm.Int(value)
	case int16:
		return vm.Int(value)
	case uint16:
		return vm.Int(value)
	case int32:
		return vm.Int(value)
	case uint32:
		return vm.Int(value)
	case int64:
		return vm.Int(value)
	case uint64:
		return vm.Int(value)
	case float32:
		return vm.Float(value)
	case float64:
		return vm.Float(value)
	case string:
		return vm.String(value)
	case []byte:
		return vm.String(value)
	case bool:
		return vm.Bool(value)
	default:
		var arr vm.Array

		switch reflect.TypeOf(value).Kind() {
		case reflect.Map:
			iter := reflect.ValueOf(value).MapRange()

			for iter.Next() {
				k := iter.Key().Interface()
				v := iter.Value().Interface()

				arr[convertValue(k)] = convertValue(v)
			}

			return arr
		case reflect.Slice, reflect.Array:
			iter := reflect.ValueOf(value)
			for i := 0; i < iter.Len(); i++ {
				arr[convertValue(i)] = convertValue(iter.Index(i).Interface())
			}

			return arr
		}
	}

	panic("unknown type")
}

func (e Expression) ExecuteContext(ctx context.Context, args map[string]any) vm.Value {
	consts := make(map[string]vm.Value)

	for name, value := range args {
		consts[name] = convertValue(value)
	}

	comp := compiler.NewCompiler(&compiler.Extensions{Exts: []compiler.Extension{{Constants: consts}}})
	global := vm.NewGlobalContext(ctx, nil, nil)
	fn := comp.Compile(unsafe.Slice(unsafe.StringData(string("<?php\n"+e+";")), len(e)), global)
	return global.Run(fn)
}

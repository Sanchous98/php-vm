package vm

import (
	"context"
	"github.com/stretchr/testify/assert"
	"testing"
)

func Test_argList_Map(t *testing.T) {
	type args struct {
		ctx  GlobalContext
		args []Value
	}
	tests := [...]struct {
		name  string
		a     argList
		args  args
		want  []Value
		want1 Throwable
	}{
		{
			"($x, $y)",
			[]Arg{{Name: "x"}, {Name: "y"}},
			args{NewGlobalContext(context.TODO(), nil, nil), []Value{Int(0), Int(1)}},
			[]Value{Int(0), Int(1)},
			nil,
		},
		{
			"(int $x, float $y)",
			[]Arg{{Name: "x", Type: IntType}, {Name: "y", Type: FloatType}},
			args{NewGlobalContext(context.TODO(), nil, nil), []Value{Int(0), Int(1)}},
			[]Value{Int(0), Float(1)},
			nil,
		},
		{
			"(int $x, int ...$y)",
			[]Arg{{Name: "x", Type: IntType}, {Name: "y", Type: IntType, Variadic: true}},
			args{NewGlobalContext(context.TODO(), nil, nil), []Value{Int(0), Int(1)}},
			[]Value{Int(0), NewArray(map[Value]Value{Int(0): Int(1)}, 1)},
			nil,
		},
	}
	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := tt.a.Map(&tt.args.ctx, tt.args.args)
			assert.Equalf(t, tt.want, got, "Map(%v, %v)", &tt.args.ctx, tt.args.args)
			assert.Equalf(t, tt.want1, got1, "Map(%v, %v)", &tt.args.ctx, tt.args.args)
		})
	}
}

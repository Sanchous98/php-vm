package internal

import "github.com/VKCOM/php-parser/pkg/ast"

const (
	ArrayRead = iota
	ArrayWrite
)

type ArrayOp struct {
	ops map[ast.Vertex]int
}

func NewArrayOps() *ArrayOp {
	return &ArrayOp{map[ast.Vertex]int{}}
}

func (op *ArrayOp) Op(vertex ast.Vertex) {
	switch vertex.(type) {
	case *ast.ExprArrayDimFetch:
	}
}

func (op *ArrayOp) Read(vertex ast.Vertex) {
	op.ops[vertex] = ArrayRead
}

func (op *ArrayOp) Write(vertex ast.Vertex) {
	op.ops[vertex] = ArrayWrite
}

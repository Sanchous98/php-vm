package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	_ "github.com/VKCOM/php-parser/pkg/visitor/traverser"
)

type Compiler struct{}

func New(node ast.Vertex) *Compiler {
	return new(Compiler)
}

package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor/traverser"
)

type Compiler struct{
	*traverser.Traverser
}

func New(node *ast.Root) *Compiler {
	c := &Compiler{}
    c.Traverser = traverser.NewTraverser(c)

	return c
}

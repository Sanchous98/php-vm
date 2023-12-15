package internal

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/visitor/nsresolver"
)

type NameResolver struct {
	*nsresolver.NamespaceResolver

	Variables map[ast.Vertex]string
}

func NewNameResolver(ns *nsresolver.NamespaceResolver) *NameResolver {
	return &NameResolver{NamespaceResolver: ns, Variables: make(map[ast.Vertex]string)}
}

func (r *NameResolver) Resolve(n ast.Vertex, aliasType string) {
	if aliasType == "variable" {
		switch n.(type) {
		case *ast.Identifier:
			r.Variables[n] = string(n.(*ast.Identifier).Value)
		case *ast.ExprVariable:
			r.Resolve(n.(*ast.ExprVariable).Name, aliasType)
			r.Variables[n] = r.Variables[n.(*ast.ExprVariable).Name]
		default:
			// TODO:
		}

		return
	}

	switch n.(type) {
	case *ast.Identifier:
		r.NamespaceResolver.ResolvedNames[n] = string(n.(*ast.Identifier).Value)
	default:
		r.NamespaceResolver.ResolveName(n, aliasType)
	}
}

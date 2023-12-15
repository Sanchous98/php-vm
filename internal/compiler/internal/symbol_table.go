package internal

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"slices"
)

type GlobalSymbolTable struct {
	Classes   []string
	Functions []string
}

type SymbolTable struct {
	prev   *SymbolTable
	global *GlobalSymbolTable

	Vars []string
}

func (s *SymbolTable) VariableName(i int) string { return s.Vars[i] }
func (s *SymbolTable) FunctionName(i int) string { return s.global.Functions[i] }
func (s *SymbolTable) ClassName(i int) string    { return s.global.Classes[i] }

func (s *SymbolTable) VariableByName(name string) int { return slices.Index(s.Vars, name) }
func (s *SymbolTable) FunctionByName(name string) int { return slices.Index(s.global.Functions, name) }
func (s *SymbolTable) ClassByName(name string) int    { return slices.Index(s.global.Classes, name) }

func (s *SymbolTable) Resolve(node ast.Vertex) {

}

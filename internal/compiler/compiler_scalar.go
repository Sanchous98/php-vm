package compiler

import (
	"github.com/VKCOM/php-parser/pkg/ast"
	"php-vm/internal/vm"
	"strconv"
	"unsafe"
)

func (c *Compiler) ScalarLnumber(n *ast.ScalarLnumber) {
	i, _ := strconv.Atoi(unsafe.String(unsafe.SliceData(n.Value), len(n.Value)))

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Literal(n, vm.Int(i))))
}

func (c *Compiler) ScalarString(n *ast.ScalarString) {
	if n.Value[0] == n.Value[len(n.Value)-1] {
		switch n.Value[0] {
		case '"', '\'', '`':
			n.Value = n.Value[1 : len(n.Value)-1]
		}
	}

	s := posixReplacer.Replace(unsafe.String(unsafe.SliceData(n.Value), len(n.Value)))

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Literal(n, vm.String(s))))
	*c.context.Bytecode() = append(*c.context.Bytecode())
}

func (c *Compiler) ScalarDnumber(n *ast.ScalarDnumber) {
	f, _ := strconv.ParseFloat(unsafe.String(unsafe.SliceData(n.Value), len(n.Value)), 64)
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Literal(n, vm.Float(f))))
}

func (c *Compiler) ScalarEncapsed(n *ast.ScalarEncapsed) {
	for _, part := range n.Parts {
		part.Accept(c)
	}
}

func (c *Compiler) ScalarEncapsedStringPart(n *ast.ScalarEncapsedStringPart) {
	s := unsafe.String(unsafe.SliceData(n.Value), len(n.Value))
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConst)<<32+uint64(c.context.Literal(n, vm.String(s))))
	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConcat)<<32)
}

func (c *Compiler) ScalarEncapsedStringVar(n *ast.ScalarEncapsedStringVar) {
	panic("not implemented")
}

func (c *Compiler) ScalarEncapsedStringBrackets(n *ast.ScalarEncapsedStringBrackets) {
	n.Var.Accept(c)

	*c.context.Bytecode() = append(*c.context.Bytecode(), uint64(vm.OpConcat)<<32)
}

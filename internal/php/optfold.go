package php

import (
	"fmt"
	"math"

	"phpvm/internal/vm"
)

// Свёртка константных выражений на этапе компиляции. Работает только с
// литеральными операндами (и результатами предыдущих свёрток), поэтому не
// может изменить порядок побочных эффектов и не выполняется для операций,
// способных бросить исключение/предупреждение (деление, сдвиги, приведение
// типов и т.п.).

// ckind — вид свёрнутого значения.
type ckind uint8

const (
	ckInt ckind = iota
	ckFloat
	ckString
	ckBool
	ckNull
)

// cval — константное значение на этапе свёртки.
type cval struct {
	k ckind
	i int64
	f float64
	s string
	b bool
}

// foldTruthy повторяет булево приведение PHP.
func foldTruthy(v cval) bool {
	switch v.k {
	case ckNull:
		return false
	case ckBool:
		return v.b
	case ckInt:
		return v.i != 0
	case ckFloat:
		return v.f != 0
	case ckString:
		return v.s != "" && v.s != "0"
	}
	return false
}

func foldAdd(a, b int64) (int64, bool) {
	c := a + b
	if (b > 0 && c < a) || (b < 0 && c > a) {
		return 0, false
	}
	return c, true
}

func foldSub(a, b int64) (int64, bool) {
	c := a - b
	if (b > 0 && c > a) || (b < 0 && c < a) {
		return 0, false
	}
	return c, true
}

func foldMul(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		return 0, false
	}
	c := a * b
	if c/b != a {
		return 0, false
	}
	return c, true
}

// foldTri возвращает -1/0/1 для a < b / == / >.
func foldTri(c int) cval {
	if c < 0 {
		return cval{k: ckInt, i: -1}
	}
	if c > 0 {
		return cval{k: ckInt, i: 1}
	}
	return cval{k: ckInt}
}

func foldCompareInt(op string, a, b int64) (cval, bool) {
	switch op {
	case "<":
		return cval{k: ckBool, b: a < b}, true
	case "<=":
		return cval{k: ckBool, b: a <= b}, true
	case ">":
		return cval{k: ckBool, b: a > b}, true
	case ">=":
		return cval{k: ckBool, b: a >= b}, true
	case "==", "===":
		return cval{k: ckBool, b: a == b}, true
	case "!=", "<>", "!==":
		return cval{k: ckBool, b: a != b}, true
	case "<=>":
		switch {
		case a < b:
			return foldTri(-1), true
		case a > b:
			return foldTri(1), true
		}
		return foldTri(0), true
	}
	return cval{}, false
}

func foldCompareFloat(op string, a, b float64) (cval, bool) {
	switch op {
	case "<":
		return cval{k: ckBool, b: a < b}, true
	case "<=":
		return cval{k: ckBool, b: a <= b}, true
	case ">":
		return cval{k: ckBool, b: a > b}, true
	case ">=":
		return cval{k: ckBool, b: a >= b}, true
	case "==", "===":
		return cval{k: ckBool, b: a == b}, true
	case "!=", "<>", "!==":
		return cval{k: ckBool, b: a != b}, true
	}
	return cval{}, false
}

// foldConstExpr пытается вычислить выражение в константу. Второй результат —
// удалось ли. Операнды обязаны быть литералами (или свёрнутыми выражениями);
// операции с возможными исключениями/предупреждениями не сворачиваются.
func foldConstExpr(e Expr) (cval, bool) {
	switch x := e.(type) {
	case *IntLit:
		return cval{k: ckInt, i: x.V}, true
	case *FloatLit:
		return cval{k: ckFloat, f: x.V}, true
	case *StrLit:
		return cval{k: ckString, s: x.V}, true
	case *BoolLit:
		return cval{k: ckBool, b: x.V}, true
	case *NullLit:
		return cval{k: ckNull}, true
	case *UnaryExpr:
		v, ok := foldConstExpr(x.X)
		if !ok {
			return cval{}, false
		}
		return foldUnary(x.Op, v)
	case *LogicalExpr:
		return foldLogicalConst(x)
	case *BinExpr:
		l, ok := foldConstExpr(x.L)
		if !ok {
			return cval{}, false
		}
		r, ok := foldConstExpr(x.R)
		if !ok {
			return cval{}, false
		}
		return foldBinary(x.Op, l, r)
	}
	return cval{}, false
}

func foldUnary(op string, v cval) (cval, bool) {
	switch op {
	case "+":
		if v.k == ckInt || v.k == ckFloat {
			return v, true
		}
	case "-":
		switch v.k {
		case ckInt:
			if v.i == math.MinInt64 {
				return cval{k: ckFloat, f: -float64(v.i)}, true
			}
			return cval{k: ckInt, i: -v.i}, true
		case ckFloat:
			return cval{k: ckFloat, f: -v.f}, true
		}
	case "!":
		return cval{k: ckBool, b: !foldTruthy(v)}, true
	case "~":
		if v.k == ckInt {
			return cval{k: ckInt, i: ^v.i}, true
		}
	}
	return cval{}, false
}

func foldBinary(op string, l, r cval) (cval, bool) {
	switch op {
	case ".":
		if l.k == ckString && r.k == ckString {
			return cval{k: ckString, s: l.s + r.s}, true
		}
	case "+", "-", "*":
		if l.k == ckInt && r.k == ckInt {
			var res int64
			var ok bool
			switch op {
			case "+":
				res, ok = foldAdd(l.i, r.i)
			case "-":
				res, ok = foldSub(l.i, r.i)
			case "*":
				res, ok = foldMul(l.i, r.i)
			}
			if ok {
				return cval{k: ckInt, i: res}, true
			}
			return cval{}, false
		}
		if l.k == ckFloat && r.k == ckFloat {
			switch op {
			case "+":
				return cval{k: ckFloat, f: l.f + r.f}, true
			case "-":
				return cval{k: ckFloat, f: l.f - r.f}, true
			case "*":
				return cval{k: ckFloat, f: l.f * r.f}, true
			}
		}
	case "&", "|", "^":
		if l.k == ckInt && r.k == ckInt {
			switch op {
			case "&":
				return cval{k: ckInt, i: l.i & r.i}, true
			case "|":
				return cval{k: ckInt, i: l.i | r.i}, true
			case "^":
				return cval{k: ckInt, i: l.i ^ r.i}, true
			}
		}
	case "==", "===", "!=", "<>", "!==", "<", "<=", ">", ">=", "<=>":
		if l.k == ckInt && r.k == ckInt {
			return foldCompareInt(op, l.i, r.i)
		}
		if l.k == ckFloat && r.k == ckFloat {
			return foldCompareFloat(op, l.f, r.f)
		}
		if l.k == ckString && r.k == ckString {
			// Только тождество: == строк в PHP 8 зависит от «числовости».
			switch op {
			case "===":
				return cval{k: ckBool, b: l.s == r.s}, true
			case "!==":
				return cval{k: ckBool, b: l.s != r.s}, true
			}
		}
		if l.k == ckBool && r.k == ckBool {
			switch op {
			case "==", "===":
				return cval{k: ckBool, b: l.b == r.b}, true
			case "!=", "<>", "!==":
				return cval{k: ckBool, b: l.b != r.b}, true
			}
		}
		if l.k == ckNull && r.k == ckNull {
			switch op {
			case "==", "===", "<=", ">=":
				return cval{k: ckBool, b: true}, true
			case "!=", "<>", "!==", "<", ">":
				return cval{k: ckBool, b: false}, true
			}
		}
	}
	return cval{}, false
}

func foldLogicalConst(x *LogicalExpr) (cval, bool) {
	switch x.Op {
	case "xor":
		l, ok := foldConstExpr(x.L)
		if !ok {
			return cval{}, false
		}
		r, ok := foldConstExpr(x.R)
		if !ok {
			return cval{}, false
		}
		return cval{k: ckBool, b: foldTruthy(l) != foldTruthy(r)}, true
	}
	isAnd := x.Op == "&&" || x.Op == "and"
	l, ok := foldConstExpr(x.L)
	if !ok {
		return cval{}, false
	}
	lb := foldTruthy(l)
	// short-circuit: правая часть не вычисляется — её можно не сворачивать.
	// Но если в ней есть `yield`, свёртка изменила бы признак генератора.
	if isAnd && !lb {
		if containsYieldExpr(x.R) {
			return cval{}, false
		}
		return cval{k: ckBool, b: false}, true
	}
	if !isAnd && lb {
		if containsYieldExpr(x.R) {
			return cval{}, false
		}
		return cval{k: ckBool, b: true}, true
	}
	r, ok := foldConstExpr(x.R)
	if !ok {
		return cval{}, false
	}
	return cval{k: ckBool, b: foldTruthy(r)}, true
}

// emitCVal материализует константное значение в свежий регистр.
func (c *compiler) emitCVal(v cval) uint8 {
	r := c.alloc()
	c.emitCValInto(v, r)
	return r
}

// emitCValInto материализует константное значение в заданный регистр.
func (c *compiler) emitCValInto(v cval, dst uint8) {
	switch v.k {
	case ckNull:
		c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: dst})
	case ckInt:
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: dst, C: int32(c.chunk.AddConst(vm.IntConst(v.i)))})
	case ckFloat:
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: dst, C: int32(c.chunk.AddConst(vm.FloatConst(v.f)))})
	case ckString:
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: dst, C: int32(c.chunk.AddConst(vm.StrConst(v.s)))})
	case ckBool:
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: dst, C: int32(c.chunk.AddConst(vm.BoolConst(v.b)))})
	default:
		panic(fmt.Sprintf("fold: неизвестный вид константы %d", v.k))
	}
}

// foldableShape — выражения, для которых имеет смысл пробовать свёртку
// (литералы обрабатываются отдельными ветками expr()).
func foldableShape(e Expr) bool {
	switch e.(type) {
	case *BinExpr, *UnaryExpr, *LogicalExpr:
		return true
	}
	return false
}

// containsYieldExpr сообщает, содержит ли выражение синтаксический `yield`.
// Во вложенные замыкания/стрелочные функции не заглядывает: их `yield`
// принадлежит другой функции (и делает генератором её, а не текущую).
func containsYieldExpr(e Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *YieldExpr:
		return true
	case *ClosureExpr, *ArrowFnExpr:
		return false
	case *BinExpr:
		return containsYieldExpr(x.L) || containsYieldExpr(x.R)
	case *LogicalExpr:
		return containsYieldExpr(x.L) || containsYieldExpr(x.R)
	case *UnaryExpr:
		return containsYieldExpr(x.X)
	case *IncDecExpr:
		return containsYieldExpr(x.X)
	case *AssignExpr:
		return containsYieldExpr(x.Target) || containsYieldExpr(x.Value)
	case *TernaryExpr:
		return containsYieldExpr(x.Cond) || containsYieldExpr(x.Then) || containsYieldExpr(x.Else)
	case *CoalesceExpr:
		return containsYieldExpr(x.L) || containsYieldExpr(x.R)
	case *IndexExpr:
		return containsYieldExpr(x.Arr) || containsYieldExpr(x.Key)
	case *PropExpr:
		return containsYieldExpr(x.Recv) || containsYieldExpr(x.NameExpr)
	case *StaticPropExpr:
		return containsYieldExpr(x.NameExpr)
	case *NamedArgExpr:
		return containsYieldExpr(x.X)
	case *ArgSpreadExpr:
		return containsYieldExpr(x.X)
	case *CallExpr:
		return exprsContainYield(x.Args)
	case *MethodCallExpr:
		return containsYieldExpr(x.Recv) || containsYieldExpr(x.NameExpr) || exprsContainYield(x.Args)
	case *StaticCallExpr:
		return containsYieldExpr(x.NameExpr) || exprsContainYield(x.Args)
	case *NewExpr:
		return containsYieldExpr(x.ClassExpr) || exprsContainYield(x.Args)
	case *NewAnonClassExpr:
		return exprsContainYield(x.Args)
	case *DynCallExpr:
		return containsYieldExpr(x.Name) || exprsContainYield(x.Args)
	case *ArrayLit:
		for _, el := range x.Elems {
			if containsYieldExpr(el.Key) || containsYieldExpr(el.Val) {
				return true
			}
		}
	case *MatchExpr:
		if containsYieldExpr(x.Subject) {
			return true
		}
		for _, arm := range x.Arms {
			for _, c := range arm.Conds {
				if containsYieldExpr(c) {
					return true
				}
			}
			if containsYieldExpr(arm.Body) {
				return true
			}
		}
	case *NullsafeExpr:
		return containsYieldExpr(x.Recv) || containsYieldExpr(x.Body)
	case *CastExpr:
		return containsYieldExpr(x.X)
	case *ListExpr:
		for _, el := range x.Elems {
			if containsYieldExpr(el.Key) || containsYieldExpr(el.Target) {
				return true
			}
		}
	case *IssetExpr:
		return exprsContainYield(x.Args)
	case *EmptyExpr:
		return containsYieldExpr(x.X)
	case *PrintExpr:
		return containsYieldExpr(x.X)
	case *ExitExpr:
		return containsYieldExpr(x.X)
	case *CloneExpr:
		return containsYieldExpr(x.X)
	case *InstanceOfExpr:
		if x.ClassExpr != nil && containsYieldExpr(x.ClassExpr) {
			return true
		}
		return containsYieldExpr(x.X)
	case *IncludeExpr:
		return containsYieldExpr(x.Path)
	}
	return false
}

func exprsContainYield(es []Expr) bool {
	for _, e := range es {
		if containsYieldExpr(e) {
			return true
		}
	}
	return false
}

// containsYieldStmts сообщает, содержит ли список операторов `yield`
// (не заглядывая во вложенные функции/замыкания).
func containsYieldStmts(list []Stmt) bool {
	for _, st := range list {
		if containsYieldStmt(st) {
			return true
		}
	}
	return false
}

func containsYieldStmt(st Stmt) bool {
	switch x := st.(type) {
	case nil:
		return false
	case *BlockStmt:
		return containsYieldStmts(x.Body)
	case *EchoStmt:
		return exprsContainYield(x.Args)
	case *ExprStmt:
		return containsYieldExpr(x.X)
	case *IfStmt:
		return containsYieldExpr(x.Cond) || containsYieldStmts(x.Then) || containsYieldStmts(x.Else)
	case *WhileStmt:
		return containsYieldExpr(x.Cond) || containsYieldStmts(x.Body)
	case *DoWhileStmt:
		return containsYieldStmts(x.Body) || containsYieldExpr(x.Cond)
	case *ForStmt:
		return exprsContainYield(x.Init) || exprsContainYield(x.Cond) ||
			exprsContainYield(x.Post) || containsYieldStmts(x.Body)
	case *ForeachStmt:
		return containsYieldExpr(x.Expr) || containsYieldExpr(x.KeyTarget) ||
			containsYieldExpr(x.ValTarget) || containsYieldStmts(x.Body)
	case *SwitchStmt:
		if containsYieldExpr(x.Subject) {
			return true
		}
		for _, cs := range x.Cases {
			if containsYieldExpr(cs.Cond) || containsYieldStmts(cs.Body) {
				return true
			}
		}
	case *ReturnStmt:
		return containsYieldExpr(x.X)
	case *ThrowStmt:
		return containsYieldExpr(x.X)
	case *TryStmt:
		if containsYieldStmts(x.Body) || containsYieldStmts(x.Finally) {
			return true
		}
		for _, cc := range x.Catches {
			if containsYieldStmts(cc.Body) {
				return true
			}
		}
	case *UnsetStmt:
		return exprsContainYield(x.Args)
	case *StaticVarStmt:
		return exprsContainYield(x.Default)
	}
	return false
}

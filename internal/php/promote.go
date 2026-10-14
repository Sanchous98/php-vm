package php

// Анализ промоушена main-переменных в регистры кадра.
//
// Идея: если main-переменная не наблюдаема извне (не участвует в ссылках,
// dynamic-доступе, `global`, `$GLOBALS`, extract/compact/…, runtime include/eval)
// и гарантированно определена во всех точках чтения, её можно держать не в
// VM.globals, а в обычном регистре кадра (как локальные функции). Это убирает
// GLOBAL_GET/GLOBAL_SET (map-lookup по строке) из горячего пути.
//
// Анализ definite-assignment консервативен: ветвления/циклы не добавляют
// переменную в «точно определена», если она присвоена не на всех путях.
// Непромотируемые переменные остаются на старом пути через VM.globals —
// это сохраняет всю семантику Undefined variable / unset / $GLOBALS.

// superglobalNames — имена, которые всегда читаются из глобальной таблицы
// (их может предустановить встраивающая программа/раннер до Run).
var superglobalNames = map[string]bool{
	"$GLOBALS": true, "$_GET": true, "$_POST": true, "$_COOKIE": true,
	"$_SERVER": true, "$_ENV": true, "$_REQUEST": true, "$_FILES": true,
	"$_SESSION": true, "$argv": true, "$argc": true, "$http_response_header": true,
}

// badScan собирает признаки, при которых промоушен main невозможен или
// конкретные имена небезопасны.
type badScan struct {
	allBad bool            // динамический доступ/$GLOBALS/include/eval/… => выключить промоушен
	bad    map[string]bool // имена, которые нельзя промотировать
}

func newBadScan() *badScan {
	return &badScan{bad: map[string]bool{}}
}

func (b *badScan) name(n string) {
	if n == "" || n == "$this" || n == "this" {
		return
	}
	if superglobalNames[n] {
		b.bad[n] = true
		return
	}
	b.bad[n] = true
}

// scanStmts обходит операторы, рекурсивно заходя в тела функций/классов/
// замыканий (они — отдельные области, но их `global`/dynamic-доступ влияет
// на наблюдаемость main-переменных).
func (b *badScan) scanStmts(list []Stmt) {
	for _, st := range list {
		b.scanStmt(st)
	}
}

func (b *badScan) scanStmt(st Stmt) {
	switch s := st.(type) {
	case nil:
	case *BlockStmt:
		b.scanStmts(s.Body)
	case *EchoStmt:
		for _, e := range s.Args {
			b.scanExpr(e)
		}
	case *ExprStmt:
		b.scanExpr(s.X)
	case *IfStmt:
		b.scanExpr(s.Cond)
		b.scanStmts(s.Then)
		b.scanStmts(s.Else)
	case *WhileStmt:
		b.scanExpr(s.Cond)
		b.scanStmts(s.Body)
	case *DoWhileStmt:
		b.scanStmts(s.Body)
		b.scanExpr(s.Cond)
	case *ForStmt:
		for _, e := range s.Init {
			b.scanExpr(e)
		}
		for _, e := range s.Cond {
			b.scanExpr(e)
		}
		for _, e := range s.Post {
			b.scanExpr(e)
		}
		b.scanStmts(s.Body)
	case *ForeachStmt:
		b.scanExpr(s.Expr)
		if s.ByRef {
			b.name(s.Val)
		}
		if s.KeyTarget != nil {
			b.scanExpr(s.KeyTarget)
		}
		if s.ValTarget != nil {
			b.scanExpr(s.ValTarget)
		}
		b.scanStmts(s.Body)
	case *SwitchStmt:
		b.scanExpr(s.Subject)
		for _, cs := range s.Cases {
			b.scanExpr(cs.Cond)
			b.scanStmts(cs.Body)
		}
	case *ReturnStmt:
		b.scanExpr(s.X)
	case *ThrowStmt:
		b.scanExpr(s.X)
	case *UnsetStmt:
		for _, e := range s.Args {
			b.scanExpr(e)
		}
	case *GlobalStmt:
		for _, n := range s.Names {
			b.name(n)
		}
		if len(s.Dyn) > 0 {
			// `global $$x` / `global ${expr}`: цель привязки неизвестна —
			// любая main-переменная может стать глобальной.
			b.allBad = true
			for _, e := range s.Dyn {
				b.scanExpr(e)
			}
		}
	case *StaticVarStmt:
		for _, e := range s.Default {
			b.scanExpr(e)
		}
	case *TryStmt:
		b.scanStmts(s.Body)
		for _, c := range s.Catches {
			b.name(c.Var)
			b.scanStmts(c.Body)
		}
		b.scanStmts(s.Finally)
	case *FuncDecl:
		b.scanStmts(s.Body)
	case *ClassDecl:
		for _, m := range s.Members {
			if len(m.Body) > 0 {
				b.scanStmts(m.Body)
			}
		}
	}
}

func (b *badScan) scanExpr(e Expr) {
	switch x := e.(type) {
	case nil:
	case *VarExpr:
		if x.NameExpr != nil {
			// $$x / ${expr}: имя неизвестно — main-переменные могут быть
			// прочитаны/записаны динамически.
			b.allBad = true
			b.scanExpr(x.NameExpr)
			return
		}
		if x.Name == "$GLOBALS" {
			b.allBad = true
		}
	case *IndexExpr:
		b.scanExpr(x.Arr)
		b.scanExpr(x.Key)
	case *PropExpr:
		b.scanExpr(x.Recv)
		b.scanExpr(x.NameExpr)
	case *StaticPropExpr:
		b.scanExpr(x.NameExpr)
	case *BinExpr:
		b.scanExpr(x.L)
		b.scanExpr(x.R)
	case *LogicalExpr:
		b.scanExpr(x.L)
		b.scanExpr(x.R)
	case *UnaryExpr:
		b.scanExpr(x.X)
	case *IncDecExpr:
		b.scanExpr(x.X)
	case *TernaryExpr:
		b.scanExpr(x.Cond)
		b.scanExpr(x.Then)
		b.scanExpr(x.Else)
	case *CoalesceExpr:
		b.scanExpr(x.L)
		b.scanExpr(x.R)
	case *AssignExpr:
		if x.ByRef {
			b.markRef(x.Target)
			b.markRef(x.Value)
		}
		b.scanExpr(x.Target)
		b.scanExpr(x.Value)
	case *ArrayLit:
		for _, el := range x.Elems {
			if el.ByRef {
				b.markRef(el.Val)
			}
			b.scanExpr(el.Key)
			b.scanExpr(el.Val)
		}
	case *MatchExpr:
		b.scanExpr(x.Subject)
		for _, arm := range x.Arms {
			for _, cond := range arm.Conds {
				b.scanExpr(cond)
			}
			b.scanExpr(arm.Body)
		}
	case *NullsafeExpr:
		b.scanExpr(x.Recv)
		b.scanExpr(x.Body)
	case *NullsafeBase:
	case *CallExpr:
		if x.Name == "extract" || x.Name == "compact" || x.Name == "get_defined_vars" || x.Name == "parse_str" || x.Name == "eval" {
			b.allBad = true
		}
		for _, a := range x.Args {
			b.scanExpr(a)
		}
	case *NamedArgExpr:
		b.scanExpr(x.X)
	case *MethodCallExpr:
		b.scanExpr(x.Recv)
		b.scanExpr(x.NameExpr)
		for _, a := range x.Args {
			b.scanExpr(a)
		}
	case *StaticCallExpr:
		b.scanExpr(x.NameExpr)
		for _, a := range x.Args {
			b.scanExpr(a)
		}
	case *NewExpr:
		b.scanExpr(x.ClassExpr)
		for _, a := range x.Args {
			b.scanExpr(a)
		}
	case *NewAnonClassExpr:
		if x.Decl != nil {
			for _, m := range x.Decl.Members {
				if len(m.Body) > 0 {
					b.scanStmts(m.Body)
				}
			}
		}
		for _, a := range x.Args {
			b.scanExpr(a)
		}
	case *DynCallExpr:
		b.scanExpr(x.Name)
		for _, a := range x.Args {
			b.scanExpr(a)
		}
	case *CastExpr:
		b.scanExpr(x.X)
	case *ListExpr:
		for _, el := range x.Elems {
			b.scanExpr(el.Key)
			b.scanExpr(el.Target)
		}
	case *IssetExpr:
		for _, a := range x.Args {
			b.scanExpr(a)
		}
	case *EmptyExpr:
		b.scanExpr(x.X)
	case *PrintExpr:
		b.scanExpr(x.X)
	case *ExitExpr:
		b.scanExpr(x.X)
	case *CloneExpr:
		b.scanExpr(x.X)
	case *InstanceOfExpr:
		b.scanExpr(x.X)
		if x.ClassExpr != nil {
			b.scanExpr(x.ClassExpr)
		}
	case *ClosureExpr:
		for _, u := range x.Uses {
			if u.ByRef {
				b.name(u.Name)
			}
		}
		b.scanStmts(x.Body)
	case *ArrowFnExpr:
		b.scanExpr(x.Body)
	case *YieldExpr:
		b.scanExpr(x.Key)
		b.scanExpr(x.Val)
	case *IncludeExpr:
		// после инлайна литеральных include здесь остаётся runtime-вариант,
		// который читает/пишет глобальную таблицу.
		b.allBad = true
		b.scanExpr(x.Path)
	}
}

// markRef помечает переменную как участвующую в ссылке (alias/by-ref): её
// нельзя держать в регистре.
func (b *badScan) markRef(e Expr) {
	switch x := e.(type) {
	case *VarExpr:
		if x.NameExpr == nil {
			b.name(x.Name)
		} else {
			b.allBad = true
		}
	case *ListExpr:
		for _, el := range x.Elems {
			b.markRef(el.Target)
		}
	}
}

// analyzeMainPromotion возвращает множество main-переменных, которые можно
// держать в регистрах.
func analyzeMainPromotion(stmts []Stmt) map[string]bool {
	b := newBadScan()
	b.scanStmts(stmts)
	if b.allBad {
		return map[string]bool{}
	}
	a := &promoAnalyzer{bad: b.bad, unsafe: map[string]bool{}}
	a.stmts(stmts, map[string]bool{})
	out := map[string]bool{}
	// Промотируем всякую простую main-переменную, которая нигде не оказалась
	// «неопределённой» и не в списке bad. Наличие других (небезопасных) имён
	// не мешает промотировать безопасные.
	// Множество кандидатов соберём повторным обходом: все VarExpr-имена.
	for _, n := range mainVarNames(stmts) {
		if b.bad[n] || a.unsafe[n] {
			continue
		}
		out[n] = true
	}
	return out
}

// promoAnalyzer выполняет definite-assignment обход main-области.
type promoAnalyzer struct {
	bad    map[string]bool
	unsafe map[string]bool
}

func copyDef(m map[string]bool) map[string]bool {
	n := make(map[string]bool, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}

func intersectDef(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

// read фиксирует чтение переменной.
func (a *promoAnalyzer) read(n string, def map[string]bool) {
	if n == "" || n == "$this" || n == "this" {
		return
	}
	if !def[n] {
		a.unsafe[n] = true
	}
}

// stmts обрабатывает список операторов, возвращая множество определённых
// после него переменных.
func (a *promoAnalyzer) stmts(list []Stmt, def map[string]bool) map[string]bool {
	for _, st := range list {
		def = a.stmt(st, def)
	}
	return def
}

func (a *promoAnalyzer) stmt(st Stmt, def map[string]bool) map[string]bool {
	switch s := st.(type) {
	case nil:
	case *BlockStmt:
		return a.stmts(s.Body, def)
	case *EchoStmt:
		for _, e := range s.Args {
			a.expr(e, def)
		}
	case *ExprStmt:
		a.expr(s.X, def)
		a.assignFromExpr(s.X, def)
	case *IfStmt:
		a.expr(s.Cond, def)
		td := a.stmts(s.Then, copyDef(def))
		ed := a.stmts(s.Else, copyDef(def))
		return intersectDef(td, ed)
	case *WhileStmt:
		a.expr(s.Cond, def)
		a.stmts(s.Body, copyDef(def))
	case *DoWhileStmt:
		bd := a.stmts(s.Body, copyDef(def))
		a.expr(s.Cond, bd)
		return bd
	case *ForStmt:
		d := def
		for _, e := range s.Init {
			a.expr(e, d)
			a.assignFromExpr(e, d)
		}
		for _, e := range s.Cond {
			a.expr(e, d)
		}
		bd := a.stmts(s.Body, copyDef(d))
		for _, e := range s.Post {
			a.expr(e, bd)
		}
		return d
	case *ForeachStmt:
		a.expr(s.Expr, def)
		bd := copyDef(def)
		if !s.ByRef {
			if s.Key != "" {
				bd[s.Key] = true
			}
			if s.Val != "" {
				bd[s.Val] = true
			}
		}
		if s.KeyTarget != nil {
			a.expr(s.KeyTarget, bd)
			a.assignFromExpr(s.KeyTarget, bd)
		}
		if s.ValTarget != nil {
			a.expr(s.ValTarget, bd)
			a.assignFromExpr(s.ValTarget, bd)
		}
		a.stmts(s.Body, bd)
	case *SwitchStmt:
		a.expr(s.Subject, def)
		for _, cs := range s.Cases {
			a.expr(cs.Cond, def)
			a.stmts(cs.Body, copyDef(def))
		}
	case *ReturnStmt:
		a.expr(s.X, def)
	case *ThrowStmt:
		a.expr(s.X, def)
	case *UnsetStmt:
		for _, e := range s.Args {
			if v, ok := e.(*VarExpr); ok && v.NameExpr == nil {
				a.bad[v.Name] = true
				delete(def, v.Name)
			} else {
				a.expr(e, def)
			}
		}
	case *GlobalStmt:
		for _, n := range s.Names {
			a.bad[n] = true
		}
	case *StaticVarStmt:
		for _, e := range s.Default {
			a.expr(e, def)
		}
		for _, n := range s.Names {
			a.bad[n] = true
		}
	case *TryStmt:
		a.stmts(s.Body, copyDef(def))
		for _, c := range s.Catches {
			cd := copyDef(def)
			if c.Var != "" {
				cd[c.Var] = true
			}
			a.stmts(c.Body, cd)
		}
		a.stmts(s.Finally, copyDef(def))
	case *FuncDecl, *ClassDecl, *LabelStmt, *GotoStmt, *BreakStmt, *ContinueStmt:
		// отдельные области / переходы: def не меняется
	}
	return def
}

// assignFromExpr добавляет определённые переменные после присваивания/инкремента.
func (a *promoAnalyzer) assignFromExpr(e Expr, def map[string]bool) {
	switch x := e.(type) {
	case *AssignExpr:
		if x.ByRef {
			a.badLValue(x.Target)
			a.badLValue(x.Value)
			return
		}
		for _, n := range lvalueNames(x.Target) {
			def[n] = true
		}
	case *IncDecExpr:
		for _, n := range lvalueNames(x.X) {
			def[n] = true
		}
	}
}

func (a *promoAnalyzer) badLValue(e Expr) {
	for _, n := range mainVarNames2(e) {
		a.bad[n] = true
	}
}

// expr фиксирует чтения в выражении.
func (a *promoAnalyzer) expr(e Expr, def map[string]bool) {
	switch x := e.(type) {
	case nil:
	case *VarExpr:
		if x.NameExpr != nil {
			a.expr(x.NameExpr, def)
			return
		}
		a.read(x.Name, def)
	case *IndexExpr:
		a.expr(x.Arr, def)
		a.expr(x.Key, def)
	case *PropExpr:
		a.expr(x.Recv, def)
		a.expr(x.NameExpr, def)
	case *StaticPropExpr:
		a.expr(x.NameExpr, def)
	case *BinExpr:
		a.expr(x.L, def)
		a.expr(x.R, def)
	case *LogicalExpr:
		a.expr(x.L, def)
		a.expr(x.R, def)
	case *UnaryExpr:
		a.expr(x.X, def)
	case *IncDecExpr:
		a.expr(x.X, def)
	case *TernaryExpr:
		a.expr(x.Cond, def)
		a.expr(x.Then, def)
		a.expr(x.Else, def)
	case *CoalesceExpr:
		a.expr(x.L, def)
		a.expr(x.R, def)
	case *AssignExpr:
		// compound-присваивание читает target; простое "=" — нет.
		if x.ByRef {
			a.badLValue(x.Target)
			a.badLValue(x.Value)
		}
		if x.Op != "=" {
			a.expr(x.Target, def)
		} else {
			a.exprNoRead(x.Target, def)
		}
		a.expr(x.Value, def)
	case *ArrayLit:
		for _, el := range x.Elems {
			if el.ByRef {
				a.badLValue(el.Val)
			}
			a.expr(el.Key, def)
			a.expr(el.Val, def)
		}
	case *MatchExpr:
		a.expr(x.Subject, def)
		for _, arm := range x.Arms {
			for _, cond := range arm.Conds {
				a.expr(cond, def)
			}
			a.expr(arm.Body, def)
		}
	case *NullsafeExpr:
		a.expr(x.Recv, def)
		a.expr(x.Body, def)
	case *NullsafeBase:
	case *CallExpr:
		for _, arg := range x.Args {
			a.expr(arg, def)
		}
	case *NamedArgExpr:
		a.expr(x.X, def)
	case *MethodCallExpr:
		a.expr(x.Recv, def)
		a.expr(x.NameExpr, def)
		for _, arg := range x.Args {
			a.expr(arg, def)
		}
	case *StaticCallExpr:
		a.expr(x.NameExpr, def)
		for _, arg := range x.Args {
			a.expr(arg, def)
		}
	case *NewExpr:
		a.expr(x.ClassExpr, def)
		for _, arg := range x.Args {
			a.expr(arg, def)
		}
	case *NewAnonClassExpr:
		for _, arg := range x.Args {
			a.expr(arg, def)
		}
	case *DynCallExpr:
		a.expr(x.Name, def)
		for _, arg := range x.Args {
			a.expr(arg, def)
		}
	case *CastExpr:
		a.expr(x.X, def)
	case *ListExpr:
		for _, el := range x.Elems {
			a.expr(el.Key, def)
			a.expr(el.Target, def)
		}
	case *IssetExpr:
		for _, arg := range x.Args {
			a.expr(arg, def)
		}
	case *EmptyExpr:
		a.expr(x.X, def)
	case *PrintExpr:
		a.expr(x.X, def)
	case *ExitExpr:
		a.expr(x.X, def)
	case *CloneExpr:
		a.expr(x.X, def)
	case *InstanceOfExpr:
		a.expr(x.X, def)
		if x.ClassExpr != nil {
			a.expr(x.ClassExpr, def)
		}
	case *ClosureExpr:
		for _, u := range x.Uses {
			if u.ByRef {
				a.bad[u.Name] = true
			} else {
				a.read(u.Name, def)
			}
		}
	case *ArrowFnExpr:
		for _, n := range arrowFreeVars(x.Body, x.Params) {
			a.read(n, def)
		}
	case *YieldExpr:
		a.expr(x.Key, def)
		a.expr(x.Val, def)
	case *IncludeExpr:
		a.expr(x.Path, def)
	}
}

// exprNoRead обходит lvalue, фиксируя чтения баз (для $a[$k] = v читаются
// только $a и $k, но не «$a[$k]» как переменная).
func (a *promoAnalyzer) exprNoRead(e Expr, def map[string]bool) {
	switch x := e.(type) {
	case *VarExpr:
		if x.NameExpr != nil {
			a.expr(x.NameExpr, def)
		}
	case *IndexExpr:
		a.expr(x.Arr, def)
		a.expr(x.Key, def)
	case *PropExpr:
		a.expr(x.Recv, def)
		a.expr(x.NameExpr, def)
	case *StaticPropExpr:
		a.expr(x.NameExpr, def)
	case *ListExpr:
		for _, el := range x.Elems {
			a.expr(el.Key, def)
			a.exprNoRead(el.Target, def)
		}
	default:
		a.expr(e, def)
	}
}

// lvalueNames возвращает простые имена переменных, которым присваивает lvalue.
func lvalueNames(e Expr) []string {
	switch x := e.(type) {
	case *VarExpr:
		if x.NameExpr == nil {
			return []string{x.Name}
		}
	case *ListExpr:
		var out []string
		for _, el := range x.Elems {
			out = append(out, lvalueNames(el.Target)...)
		}
		return out
	}
	return nil
}

// mainVarNames собирает все простые имена переменных main-области (не заходя в
// тела функций/классов/замыканий).
func mainVarNames(stmts []Stmt) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n == "" || n == "$this" || n == "this" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	var walkE func(Expr)
	var walkS func([]Stmt)
	walkE = func(e Expr) {
		switch x := e.(type) {
		case nil:
		case *VarExpr:
			if x.NameExpr == nil {
				add(x.Name)
			} else {
				walkE(x.NameExpr)
			}
		case *IndexExpr:
			walkE(x.Arr)
			walkE(x.Key)
		case *PropExpr:
			walkE(x.Recv)
			walkE(x.NameExpr)
		case *StaticPropExpr:
			walkE(x.NameExpr)
		case *BinExpr:
			walkE(x.L)
			walkE(x.R)
		case *LogicalExpr:
			walkE(x.L)
			walkE(x.R)
		case *UnaryExpr:
			walkE(x.X)
		case *IncDecExpr:
			walkE(x.X)
		case *TernaryExpr:
			walkE(x.Cond)
			walkE(x.Then)
			walkE(x.Else)
		case *CoalesceExpr:
			walkE(x.L)
			walkE(x.R)
		case *AssignExpr:
			walkE(x.Target)
			walkE(x.Value)
		case *ArrayLit:
			for _, el := range x.Elems {
				walkE(el.Key)
				walkE(el.Val)
			}
		case *MatchExpr:
			walkE(x.Subject)
			for _, arm := range x.Arms {
				for _, cond := range arm.Conds {
					walkE(cond)
				}
				walkE(arm.Body)
			}
		case *NullsafeExpr:
			walkE(x.Recv)
			walkE(x.Body)
		case *NullsafeBase:
		case *CallExpr:
			for _, arg := range x.Args {
				walkE(arg)
			}
		case *NamedArgExpr:
			walkE(x.X)
		case *MethodCallExpr:
			walkE(x.Recv)
			walkE(x.NameExpr)
			for _, arg := range x.Args {
				walkE(arg)
			}
		case *StaticCallExpr:
			walkE(x.NameExpr)
			for _, arg := range x.Args {
				walkE(arg)
			}
		case *NewExpr:
			walkE(x.ClassExpr)
			for _, arg := range x.Args {
				walkE(arg)
			}
		case *DynCallExpr:
			walkE(x.Name)
			for _, arg := range x.Args {
				walkE(arg)
			}
		case *CastExpr:
			walkE(x.X)
		case *ListExpr:
			for _, el := range x.Elems {
				walkE(el.Key)
				walkE(el.Target)
			}
		case *IssetExpr:
			for _, arg := range x.Args {
				walkE(arg)
			}
		case *EmptyExpr:
			walkE(x.X)
		case *PrintExpr:
			walkE(x.X)
		case *ExitExpr:
			walkE(x.X)
		case *CloneExpr:
			walkE(x.X)
		case *InstanceOfExpr:
			walkE(x.X)
			if x.ClassExpr != nil {
				walkE(x.ClassExpr)
			}
		case *ClosureExpr:
			for _, u := range x.Uses {
				add(u.Name)
			}
		case *ArrowFnExpr:
			for _, n := range arrowFreeVars(x.Body, x.Params) {
				add(n)
			}
		case *YieldExpr:
			walkE(x.Key)
			walkE(x.Val)
		}
	}
	walkS = func(list []Stmt) {
		for _, st := range list {
			switch s := st.(type) {
			case *BlockStmt:
				walkS(s.Body)
			case *EchoStmt:
				for _, e := range s.Args {
					walkE(e)
				}
			case *ExprStmt:
				walkE(s.X)
			case *IfStmt:
				walkE(s.Cond)
				walkS(s.Then)
				walkS(s.Else)
			case *WhileStmt:
				walkE(s.Cond)
				walkS(s.Body)
			case *DoWhileStmt:
				walkS(s.Body)
				walkE(s.Cond)
			case *ForStmt:
				for _, e := range s.Init {
					walkE(e)
				}
				for _, e := range s.Cond {
					walkE(e)
				}
				for _, e := range s.Post {
					walkE(e)
				}
				walkS(s.Body)
			case *ForeachStmt:
				walkE(s.Expr)
				if s.Key != "" {
					add(s.Key)
				}
				if s.Val != "" {
					add(s.Val)
				}
				walkE(s.KeyTarget)
				walkE(s.ValTarget)
				walkS(s.Body)
			case *SwitchStmt:
				walkE(s.Subject)
				for _, cs := range s.Cases {
					walkE(cs.Cond)
					walkS(cs.Body)
				}
			case *ReturnStmt:
				walkE(s.X)
			case *ThrowStmt:
				walkE(s.X)
			case *UnsetStmt:
				for _, e := range s.Args {
					walkE(e)
				}
			case *StaticVarStmt:
				for _, e := range s.Default {
					walkE(e)
				}
			case *TryStmt:
				walkS(s.Body)
				for _, c := range s.Catches {
					walkS(c.Body)
				}
				walkS(s.Finally)
			}
		}
	}
	walkS(stmts)
	return out
}

// mainVarNames2 — имена простых переменных в выражении (для by-ref пометки).
func mainVarNames2(e Expr) []string {
	var out []string
	switch x := e.(type) {
	case *VarExpr:
		if x.NameExpr == nil {
			out = append(out, x.Name)
		}
	case *ListExpr:
		for _, el := range x.Elems {
			out = append(out, mainVarNames2(el.Target)...)
		}
	}
	return out
}

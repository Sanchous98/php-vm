package vm

const stackSize = 512

type Stack struct {
	stack [stackSize]Value
	sp    int
}

func (s *Stack) Init() { s.sp = -1 }
func (s *Stack) Pop() (v Value) {
	v = s.stack[s.sp]
	s.sp--
	return
}
func (s *Stack) Push(v Value) {
	s.sp++
	s.stack[s.sp] = v
}
func (s *Stack) TopIndex() int                { return s.sp }
func (s *Stack) Slice(start, end int) []Value { return s.stack[s.sp+1+start : s.sp+1+end] }
func (s *Stack) Sp(sp int)                    { s.sp = sp }
func (s *Stack) Top() Value                   { return s.stack[s.sp] }
func (s *Stack) SetTop(v Value)               { s.stack[s.sp] = v }
func (s *Stack) AddI(ctx *FunctionContext) {
	ctx.sp--
	ctx.stack[ctx.sp] = ctx.stack[ctx.sp].(Addable).Add(ctx, ctx.stack[ctx.sp+1])
}

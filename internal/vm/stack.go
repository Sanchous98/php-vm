package vm

const stackSize = 512

type Stack[T any] struct {
	sp    int
	stack [stackSize]T
}

func (s *Stack[T]) Init() { s.sp = -1 }
func (s *Stack[T]) Pop() (v T) {
	v = s.stack[s.sp]
	s.sp--
	return
}
func (s *Stack[T]) Push(v T) {
	s.sp++
	s.stack[s.sp] = v
}
func (s *Stack[T]) TopIndex() int                  { return s.sp }
func (s *Stack[T]) Slice(offsetX, offsetY int) []T { return s.stack[s.sp+1+offsetX : s.sp+1+offsetY] }
func (s *Stack[T]) Sp(pointer int)                 { s.sp = pointer }
func (s *Stack[T]) Top() T                         { return s.stack[s.sp] }
func (s *Stack[T]) SetTop(v T)                     { s.stack[s.sp] = v }

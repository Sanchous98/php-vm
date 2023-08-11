package vm

import (
	"unsafe"
)

const stackSize = 512

type Stack struct {
	sp    unsafe.Pointer
	stack [stackSize]Value
}

func (s *Stack) Init() {
	s.sp = unsafe.Add(unsafe.Pointer(&s.stack[0]), -16)
}
func (s *Stack) Offset(offset int) Value {
	return *(*Value)(unsafe.Add(s.sp, offset << 4))
}
func (s *Stack) Pop() (v Value) {
    if uintptr(s.sp) < uintptr(unsafe.Pointer(&s.stack[0])) {
		panic("stack is empty")
	}

	v, *(*Value)(s.sp) = *(*Value)(s.sp), v
	s.MovePointer(-1)
	return
}
func (s *Stack) Push(v Value) {
	if s.sp == unsafe.Pointer(&s.stack[stackSize-1]) {
		panic("stack overflow")
	}

	s.MovePointer(1)
	*(*Value)(s.sp) = v
}
func (s *Stack) Put(index int, v Value) { s.stack[index] = v }
func (s *Stack) TopIndex() int {
	return int(uintptr(s.sp)-uintptr(unsafe.Pointer(&s.stack[0]))) >> 4
}
func (s *Stack) Slice(offsetX, offsetY int) []Value {
	length := s.TopIndex() + 1
	return s.stack[length+offsetX : length+offsetY]
}
func (s *Stack) Sp(pointer int) {
	s.sp = unsafe.Add(unsafe.Pointer(&s.stack[0]), pointer << 4)
}
func (s *Stack) MovePointer(offset int) {
	s.sp = unsafe.Add(s.sp, offset << 4)
}
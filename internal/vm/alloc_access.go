package vm

// NumRegs возвращает число регистров, нужных байткод-функции.
func (f *CompiledFunction) NumRegs() int { return f.chunk.NumRegs() }

// Disassemble возвращает листинг байткода функции.
func (f *CompiledFunction) Disassemble() string { return f.chunk.Disassemble() }

//go:build !vmdebug

package vm

// checkConstKinds — no-op в обычной сборке. Инвариант типов K-констант
// гарантируется компилятором (см. kassert_on.go для отладочной проверки).
func (c *Chunk) checkConstKinds() {}

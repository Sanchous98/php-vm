package vm

// execResult — сигнал, которым обработчик опкода управляет главным циклом:
//
//	next — соответствует `continue` (пропустить остаток итерации);
//	halt — соответствует `return nil` (штатное завершение exec);
//	err  — соответствует `return err` (проброс ошибки/сигнала).
//
// Нулевое значение означает обычное продолжение цикла (как `break` из switch).
type execResult struct {
	next bool
	halt bool
	err  error
}

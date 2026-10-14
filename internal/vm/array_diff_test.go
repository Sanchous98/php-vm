package vm

import (
	"os/exec"
	"strings"
	"testing"
)

// Дифференциальный тест против живого php: порядок ключей, nextFree, tombstone.
func TestArraySequenceAgainstLivePHP(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	script := `<?php
$a = [];
$a["x"] = 1; $a[10] = 2; $a["y"] = 3; $a[] = 4;
unset($a["x"]);
$a["10"] = 5;   // "10" -> int 10 (существующий)
$a[] = 6;
echo implode(",", array_keys($a));
print "\n";
print count($a);
`
	out, err := exec.Command(php, "-r", strings.TrimPrefix(script, "<?php\n")).Output()
	if err != nil {
		t.Fatalf("php: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	wantKeys, wantCount := lines[0], lines[1]

	a := newArray()
	a.Set(stringKey("x"), MakeInt(1))
	a.Set(intKey(10), MakeInt(2))
	a.Set(stringKey("y"), MakeInt(3))
	a.Append(MakeInt(4)) // 11
	a.Unset(stringKey("x"))
	a.Set(intKey(10), MakeInt(5))
	a.Append(MakeInt(6)) // 12

	if got := strings.Join(keysInOrder(a), ","); got != wantKeys {
		t.Errorf("keys = %q, php %q", got, wantKeys)
	}
	if got := intToString(int64(a.Len())); got != wantCount {
		t.Errorf("count = %s, php %s", got, wantCount)
	}
}

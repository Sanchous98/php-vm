package vm

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestConversionsAgainstLivePHP запускает testdata/casts.php настоящим php и
// строит матрицу на лету. Если php нет в PATH — пропускается.
func TestConversionsAgainstLivePHP(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	script, err := os.ReadFile("testdata/casts.php")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := dir + "/casts.php"
	if err := os.WriteFile(path, script, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(php, path).Output()
	if err != nil {
		t.Fatalf("php: %v", err)
	}

	m := map[string]map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}
		if m[parts[1]] == nil {
			m[parts[1]] = map[string]string{}
		}
		m[parts[1]][parts[0]] = parts[2]
	}

	for name, v := range referenceValues() {
		ref, ok := m[name]
		if !ok {
			t.Errorf("live php did not produce %q", name)
			continue
		}
		if got := v.Type().String(); got != goTypeName(ref["type"]) {
			t.Errorf("%s: type = %s, php %s", name, got, goTypeName(ref["type"]))
		}
		if got := boolString(v.ToBool()); got != ref["bool"] {
			t.Errorf("%s: bool = %s, php %s", name, got, ref["bool"])
		}
		if want, err := parsePHPInt(ref["int"]); err == nil && v.ToInt() != want {
			t.Errorf("%s: int = %d, php %d", name, v.ToInt(), want)
		}
		if want, err := parseFloat(ref["float"]); err == nil && v.ToFloat() != want {
			t.Errorf("%s: float = %v, php %v", name, v.ToFloat(), want)
		}
		if gotHex := hexString(v.ToString()); gotHex != ref["str"] {
			t.Errorf("%s: string = %q, php %q", name, v.ToString(), ref["str"])
		}
	}
}

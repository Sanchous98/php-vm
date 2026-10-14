package php

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"phpvm/internal/vm"
)

// --- прокси формата .phpc: сериализация потока инструкций (Instruction=Op,A,B,C) ---

func probeMarshalCode(c *vm.Chunk) []byte {
	n := c.Len()
	buf := make([]byte, 0, 2+n*7)
	var h [2]byte
	binary.LittleEndian.PutUint16(h[:], uint16(n))
	buf = append(buf, h[:]...)
	for i := 0; i < n; i++ {
		in := c.At(i)
		buf = append(buf, byte(in.Op), in.A, in.B)
		var cb [4]byte
		binary.LittleEndian.PutUint32(cb[:], uint32(in.C))
		buf = append(buf, cb[:]...)
	}
	return buf
}

func probeUnmarshalCode(b []byte) []vm.Instruction {
	if len(b) < 2 {
		return nil
	}
	n := int(binary.LittleEndian.Uint16(b[:2]))
	out := make([]vm.Instruction, n)
	p := 2
	for i := 0; i < n && p+7 <= len(b); i++ {
		out[i] = vm.Instruction{
			Op: vm.Opcode(b[p]),
			A:  b[p+1],
			B:  b[p+2],
			C:  int32(binary.LittleEndian.Uint32(b[p+3:])),
		}
		p += 7
	}
	return out
}

// TestAOTProbe оценивает выигрыш от AOT (предкомпиляция PHP→байткод и загрузка
// без этапа компиляции). Измеряет на корпусе: время полной компиляции (CompileFile),
// время исполнения (Run), и стоимость «загрузки» как маршалинг+анмаршалинг потока
// инструкций (прокси того, чем AOT заменит компиляцию).
//
//	PHPT_DIR=/path PHPT_LIMIT=250 go test -run TestAOTProbe -v ./internal/php/
func TestAOTProbe(t *testing.T) {
	if os.Getenv("PHPT_DIR") == "" && os.Getenv("AOT_PROBE") == "" {
		t.Skip("set PHPT_DIR или AOT_PROBE=1")
	}
	type rec struct {
		name      string
		compile   time.Duration
		run       time.Duration
		codeBytes int
		loadProxy time.Duration
		totalIns  int
	}

	var recs []rec
	add := func(name, src, path string) {
		var c *Compiled
		var err error
		t0 := time.Now()
		c, err = CompileFile(src, path)
		compile := time.Since(t0)
		if err != nil || c == nil || c.Main == nil {
			return
		}
		// прокси-размер и стоимость загрузки: main + все функции.
		blob := probeMarshalCode(c.Main)
		totalIns := c.Main.Len()
		for _, f := range c.Funcs {
			if cf, ok := f.Executable.(*vm.CompiledFunction); ok {
				totalIns += cf.CodeLen()
			}
		}
		tl := time.Now()
		for k := 0; k < 20; k++ {
			_ = probeUnmarshalCode(blob)
		}
		load := time.Since(tl) / 20

		// исполнение (с защитой от паник и лимитом шагов).
		var run time.Duration
		func() {
			defer func() { _ = recover() }()
			var m vm.VM
			m.Init(c.Main, c.Funcs, c.Classes)
			m.SetStepLimit(2_000_000)
			m.SetOutput(io.Discard)
			t1 := time.Now()
			_ = m.Run()
			run = time.Since(t1)
		}()
		recs = append(recs, rec{name, compile, run, len(blob), load, totalIns})
	}

	if root := os.Getenv("PHPT_DIR"); root != "" {
		limit := 250
		if v := os.Getenv("PHPT_LIMIT"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		var files []string
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
				files = append(files, p)
			}
			return nil
		})
		sort.Strings(files)
		for _, f := range files {
			if len(recs) >= limit {
				break
			}
			data, _ := os.ReadFile(f)
			p := parsePhpt(string(data))
			if p.File == "" || (!p.HasExpect && !p.HasExpectf) || p.HasSkipIf || p.HasFileExt {
				continue
			}
			add(filepath.Base(f), p.File, f)
		}
	} else {
		for i, src := range probeScripts {
			add(fmt.Sprintf("script%02d", i), src, "/tmp/aot_probe.php")
		}
	}

	var sumC, sumR, sumL time.Duration
	var sumBytes, sumIns int
	for _, r := range recs {
		sumC += r.compile
		sumR += r.run
		sumL += r.loadProxy
		sumBytes += r.codeBytes
		sumIns += r.totalIns
	}
	n := len(recs)
	if n == 0 {
		t.Skip("нет файлов для пробника")
	}
	coldInterp := sumC + sumR
	coldAOT := sumL + sumR
	share := 100 * float64(sumC) / float64(coldInterp)
	speed := float64(coldInterp) / float64(coldAOT)
	t.Logf("файлов: %d", n)
	t.Logf("СУММА: компиляция=%.1f мс, исполнение=%.1f мс, загрузка-прокси=%.1f мс",
		ms(sumC), ms(sumR), ms(sumL))
	t.Logf("инструкций: %d (в среднем %.0f/файл); сериализовано main-кода: %d байт (%.0f/файл)",
		sumIns, float64(sumIns)/float64(n), sumBytes, float64(sumBytes)/float64(n))
	t.Logf("доля компиляции в cold-start: %.1f%% (компиляция/%.1f мс против исполнение/%.1f мс)",
		share, ms(sumC), ms(sumR))
	t.Logf("cold-start: интерпретатор=%.1f мс, AOT(загрузка+исполнение)=%.1f мс → ускорение ×%.2f",
		ms(coldInterp), ms(coldAOT), speed)
	t.Logf("на 1 прогон процесса AOT экономит ≈%.1f мс/файл (компиляция-загрузка)",
		ms(sumC-sumL)/float64(n))
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

var probeScripts = []string{
	`<?php $s=0; for($i=0;$i<200000;$i++){ $s=$s+$i; } echo $s;`,
	`<?php class C { private $v=2; function get(){ return $this->v; } } $o=new C(); $s=0; for($i=0;$i<100000;$i++){ $s=$s+$o->get(); } echo $s;`,
	`<?php function f($n){ if($n<2) return $n; return f($n-1)+f($n-2); } echo f(24);`,
	`<?php $a=[]; for($i=0;$i<50000;$i++){ $a[$i]=$i*2; } echo count($a);`,
	`<?php $s=""; for($i=0;$i<20000;$i++){ $s.="x"; } echo strlen($s);`,
}

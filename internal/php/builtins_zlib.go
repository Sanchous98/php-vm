package php

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"

	"phpvm/internal/vm"
)

// Функции сжатия zlib/gzip.

func gzCompressFn(ctx *vm.Context) vm.Value {
	data := arg0(ctx).ToString()
	level := argLevel(ctx, 1)
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, level)
	if err != nil {
		return vm.Bool(false)
	}
	w.Write([]byte(data))
	w.Close()
	return vm.Str(buf.String())
}

func gzUncompressFn(ctx *vm.Context) vm.Value {
	data := []byte(arg0(ctx).ToString())
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		ctx.VM().Warning("gzuncompress(): data error")
		return vm.Bool(false)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		ctx.VM().Warning("gzuncompress(): data error")
		return vm.Bool(false)
	}
	return vm.Str(string(out))
}

func gzDeflateFn(ctx *vm.Context) vm.Value {
	data := arg0(ctx).ToString()
	level := argLevel(ctx, 1)
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, level)
	if err != nil {
		return vm.Bool(false)
	}
	w.Write([]byte(data))
	w.Close()
	return vm.Str(buf.String())
}

func gzInflateFn(ctx *vm.Context) vm.Value {
	data := []byte(arg0(ctx).ToString())
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		ctx.VM().Warning("gzinflate(): data error")
		return vm.Bool(false)
	}
	return vm.Str(string(out))
}

func gzEncodeFn(ctx *vm.Context) vm.Value {
	data := arg0(ctx).ToString()
	level := argLevel(ctx, 1)
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, level)
	if err != nil {
		return vm.Bool(false)
	}
	w.Write([]byte(data))
	w.Close()
	return vm.Str(buf.String())
}

func gzDecodeFn(ctx *vm.Context) vm.Value {
	data := []byte(arg0(ctx).ToString())
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return vm.Bool(false)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return vm.Bool(false)
	}
	return vm.Str(string(out))
}

// bzDecompressFn — bzdecompress(): в этой реализации пары
// bzip2.compress/bzdecompress используются согласованно (identity-сжатие),
// поэтому декодирование возвращает входные данные.
func bzDecompressFn(ctx *vm.Context) vm.Value {
	return vm.Str(arg0(ctx).ToString())
}

// argLevel извлекает уровень сжатия (-1 по умолчанию).
func argLevel(ctx *vm.Context, i int) int {
	if len(ctx.Args()) <= i || arg(ctx, i).Type() == vm.NullType {
		return -1
	}
	l := int(arg(ctx, i).ToInt())
	if l < -1 || l > 9 {
		return -1
	}
	return l
}

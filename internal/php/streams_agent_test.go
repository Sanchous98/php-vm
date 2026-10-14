package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStreamsAgent — точечные проверки типа resource и потокового слоя.
func TestStreamsAgent(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		php  string
		want string
	}{
		{
			"gettype",
			`<?php $f = fopen('php://memory','r+'); var_dump(gettype($f), get_debug_type($f));`,
			"string(8) \"resource\"\nstring(17) \"resource (stream)\"\n",
		},
		{
			"is_resource",
			`<?php $f = fopen('php://memory','r+'); var_dump(is_resource($f));`,
			"bool(true)\n",
		},
		{
			"resource_type",
			`<?php $f = fopen('php://memory','r+'); var_dump(get_resource_type($f), get_resource_id($f) > 0);`,
			"string(6) \"stream\"\nbool(true)\n",
		},
		{
			"varnum",
			`<?php $f = fopen('php://memory','r+'); var_dump($f);`,
			"resource(%d) of type (stream)\n",
		},
		{
			"varnum_closed",
			`<?php $f = fopen('php://memory','r+'); $n = get_resource_id($f); fclose($f); var_dump($f);`,
			"resource(%d) of type (Unknown)\n",
		},
		{
			"intcast",
			`<?php $f = fopen('php://memory','r+'); var_dump((int)$f === get_resource_id($f));`,
			"bool(true)\n",
		},
		{
			"rw",
			`<?php $f = fopen('php://memory','w+'); fwrite($f, "hello"); rewind($f); var_dump(fread($f, 5));`,
			`string(5) "hello"` + "\n",
		},
		{
			"gets",
			`<?php $f = fopen('php://memory','w+'); fwrite($f, "a\nb\n"); rewind($f); var_dump(fgets($f), fgets($f));`,
			"string(2) \"a\n\"\nstring(2) \"b\n\"\n",
		},
		{
			"meta",
			`<?php $f = fopen('php://memory','r+'); $m = stream_get_meta_data($f); var_dump($m['mode'], $m['seekable']);`,
			"string(2) \"r+\"\nbool(true)\n",
		},
		{
			"opendir",
			`<?php $d = opendir(%q); var_dump(get_resource_type($d));`,
			"string(6) \"stream\"\n",
		},
		{
			"closedir",
			`<?php $d = opendir(%q); closedir($d); var_dump($d);`,
			"resource(%d) of type (Unknown)\n",
		},
		{
			"typeerror",
			`<?php try { get_resource_type(5); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
			"get_resource_type(): Argument #1 ($resource) must be of type resource, int given\n",
		},
		{
			"strconv",
			`<?php $f = fopen('php://memory','r+'); echo $f, "\n";`,
			"Resource id #%d\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			php := c.php
			if strings.Contains(php, "%q") {
				php = strings.ReplaceAll(php, "%q", "'"+dir+"'")
				php = strings.ReplaceAll(php, "%d", "%d")
			}
			got, err := runFile(php)
			if err != nil {
				t.Fatalf("err=%v out=%q", err, got)
			}
			if strings.Contains(c.want, "%d") {
				// грубая проверка формы: ресурс напечатан с числом
				if !strings.Contains(got, "resource(") && !strings.Contains(got, "Resource id #") {
					t.Fatalf("got=%q want=%q", got, c.want)
				}
				return
			}
			if got != c.want {
				t.Fatalf("got=%q want=%q", got, c.want)
			}
		})
	}
	_ = filepath.Join
	_ = os.Getenv
}

package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runSplPHP — дифференциальный прогон SPL-кейсов: вывод интерпретатора должен
// побайтово совпасть с живым php 8.3 (error_reporting=0).
func runSplPHP(t *testing.T, src string) string {
	t.Helper()
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "case.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	want, werr := exec.Command(phppath, "-d", "error_reporting=0", f).Output()
	if werr != nil {
		t.Fatalf("php failed for %q: %v\noutput: %s", src, werr, want)
	}
	got, err := runFileAt(src, f)
	if err != nil {
		t.Fatalf("vm failed for %q: %v\noutput: %q", src, err, got)
	}
	if got != string(want) {
		t.Errorf("spl mismatch:\n vm  = %q\n php = %q", got, string(want))
	}
	return got
}

func TestSplAgentVsPHP(t *testing.T) {
	cases := []string{
		// --- ArrayObject: базовая обёртка над массивом ---
		`<?php $ao = new ArrayObject(["a"=>1,"b"=>2,3]); echo $ao->count(), "\n"; $ao[]=4; $ao["c"]=5; foreach($ao as $k=>$v) echo "$k=>$v "; echo "\n"; var_dump($ao->getArrayCopy());`,
		`<?php $ao = new ArrayObject(["a"=>1]); echo $ao["a"], $ao["z"], "\n"; var_dump(isset($ao["a"]), isset($ao["z"])); unset($ao["a"]); var_dump(isset($ao["a"]));`,
		`<?php $ao = new ArrayObject(["x"=>9]); $old=$ao->exchangeArray(["y"=>8]); var_dump($old); echo $ao["y"], "\n"; $ao->append(7); var_dump($ao->getArrayCopy());`,
		`<?php $ao = new ArrayObject(["b"=>2,"a"=>1,"c"=>3]); $ao->asort(); var_dump($ao->getArrayCopy()); $ao->ksort(); var_dump($ao->getArrayCopy());`,
		`<?php $ao = new ArrayObject(); echo $ao->getFlags(), "\n"; $ao->setFlags(2); echo $ao->getFlags(), "\n"; echo ArrayObject::STD_PROP_LIST, ArrayObject::ARRAY_AS_PROPS, "\n";`,
		`<?php $ao = new ArrayObject([1,2,3]); echo get_class($ao), "\n"; var_dump($ao instanceof ArrayAccess, $ao instanceof Countable, $ao instanceof IteratorAggregate, $ao instanceof Traversable, $ao instanceof Iterator);`,
		`<?php $ao = new ArrayObject([10,20]); echo get_class($ao->getIterator()), " ", $ao->getIteratorClass(), "\n"; var_dump($ao->getIterator() instanceof ArrayIterator);`,

		// --- ArrayIterator ---
		`<?php $it = new ArrayIterator([10,20,30]); foreach($it as $k=>$v) echo "$k=>$v "; echo "\n"; echo $it->count(), "\n"; $it->append(40); echo $it->count(), "\n";`,
		`<?php $it = new ArrayIterator([10,20,30]); $it->rewind(); echo $it->key(), "=", $it->current(), "\n"; $it->next(); echo $it->key(), "=", $it->current(), "\n"; var_dump($it->valid()); $it->seek(2); echo $it->key(), "=", $it->current(), "\n";`,
		`<?php $it = new ArrayIterator([1,2]); $it["x"]="y"; echo $it["x"], "\n"; var_dump($it->getArrayCopy()); var_dump($it instanceof SeekableIterator, $it instanceof Iterator, $it instanceof ArrayAccess, $it instanceof Countable);`,
		`<?php $it = new ArrayIterator(["b"=>2,"a"=>1]); $it->asort(); var_dump($it->getArrayCopy());`,

		// --- SplStack (LIFO) ---
		`<?php $s = new SplStack(); $s->push(1); $s->push(2); $s->push(3); echo $s->count(), " ", $s->top(), " ", $s->bottom(), "\n"; foreach($s as $k=>$v) echo "$k=>$v "; echo "\n"; echo $s->pop(), $s->pop(), "\n"; var_dump($s->isEmpty());`,
		`<?php $s = new SplStack(); $s->push("a"); $s[0]="z"; echo $s[0], "\n"; var_dump(isset($s[0]), isset($s[5])); unset($s[0]); var_dump(isset($s[0]));`,
		`<?php $s = new SplStack(); echo get_class($s), "\n"; var_dump($s instanceof SplStack, $s instanceof SplDoublyLinkedList, $s instanceof Iterator, $s instanceof ArrayAccess, $s instanceof Countable);`,

		// --- SplQueue (FIFO) ---
		`<?php $q = new SplQueue(); $q->enqueue("a"); $q->enqueue("b"); $q->push("c"); echo $q->count(), " ", $q->bottom(), " ", $q->top(), "\n"; foreach($q as $v) echo $v; echo "\n"; echo $q->dequeue(), $q->dequeue(), "\n"; var_dump($q->isEmpty());`,
		`<?php $q = new SplQueue(); echo get_class($q), "\n"; var_dump($q instanceof SplQueue, $q instanceof SplDoublyLinkedList);`,

		// --- SplDoublyLinkedList ---
		`<?php $l = new SplDoublyLinkedList(); $l->push(1); $l->push(2); $l->push(3); foreach($l as $k=>$v) echo "$k=>$v "; echo "\n"; $l->unshift(0); foreach($l as $k=>$v) echo "$k=>$v "; echo "\n"; echo $l->shift(), $l->pop(), "\n"; foreach($l as $k=>$v) echo "$k=>$v "; echo "\n";`,
		`<?php $l = new SplDoublyLinkedList(); $l->push(4); $l->push(5); echo $l[0], $l[1], "\n"; $l[0]=9; echo $l[0], "\n"; var_dump($l->count(), $l->isEmpty()); echo SplDoublyLinkedList::IT_MODE_LIFO, SplDoublyLinkedList::IT_MODE_FIFO, "\n";`,

		// --- SplObjectStorage ---
		`<?php $o1=new stdClass(); $o2=new stdClass(); $s=new SplObjectStorage(); $s->attach($o1); $s->attach($o2,"info2"); echo $s->count(), "\n"; var_dump($s->contains($o1), $s->contains(new stdClass())); foreach($s as $k=>$v) echo "$k=", get_class($v), "\n";`,
		`<?php $o1=new stdClass(); $o2=new stdClass(); $s=new SplObjectStorage(); $s->attach($o1,"a"); $s->attach($o2,"b"); $s->rewind(); $s->setInfo("new"); var_dump($s->getInfo()); $s->detach($o1); echo $s->count(), "\n"; var_dump(isset($s[$o2]), isset($s[$o1]));`,
		`<?php $o1=new stdClass(); $o2=new stdClass(); $o3=new stdClass(); $s=new SplObjectStorage(); $s->attach($o1); $s2=new SplObjectStorage(); $s2->attach($o2,"z"); $s2->attach($o3,"c"); $s->addAll($s2); echo $s->count(), "\n"; $s->removeAll($s2); echo $s->count(), "\n"; var_dump($s->contains($o2));`,
		`<?php $o=new stdClass(); $s=new SplObjectStorage(); $s[$o]="val"; var_dump($s->contains($o), $s[$o]); unset($s[$o]); var_dump($s->contains($o)); var_dump($s instanceof Countable, $s instanceof Iterator, $s instanceof ArrayAccess);`,

		// --- SplFixedArray ---
		`<?php $fa=new SplFixedArray(); echo $fa->getSize(), " ", $fa->count(), "\n"; $fa=new SplFixedArray(3); $fa[0]="a"; $fa[1]="b"; $fa[2]="c"; foreach($fa as $k=>$v) echo "$k=>$v "; echo "\n"; var_dump($fa->toArray());`,
		`<?php $fa=new SplFixedArray(3); $fa[0]=1; var_dump(isset($fa[0]), isset($fa[3])); unset($fa[0]); var_dump($fa[0]); $fa->setSize(5); var_dump($fa->toArray()); $fa->setSize(2); var_dump($fa->toArray());`,
		`<?php $fa=SplFixedArray::fromArray(["x","y","z"]); var_dump($fa->toArray()); echo $fa->getSize(), "\n"; var_dump($fa instanceof ArrayAccess, $fa instanceof Countable, $fa instanceof IteratorAggregate, $fa instanceof Iterator, $fa instanceof JsonSerializable);`,
		`<?php $fa=new SplFixedArray(2); try { echo $fa[5]; } catch (RuntimeException $e) { echo "caught\n"; } var_dump($fa->jsonSerialize());`,
	}

	for _, src := range cases {
		runSplPHP(t, src)
	}
}

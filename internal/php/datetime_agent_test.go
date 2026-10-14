package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDateTimeAgentVsPHP — дифференциальные кейсы классов даты/времени
// (DateTime, DateTimeImmutable, DateTimeZone, DateInterval, DatePeriod)
// против живого php 8.3.
func TestDateTimeAgentVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// --- DateTime: конструктор и format ---
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("@1700000000"))->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("2023-01-02 03:04:05"))->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("2023-01-02"))->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("now"))->format("e");`,
		`<?php date_default_timezone_set("Europe/Moscow"); echo (new DateTime("2023-06-01 12:00:00"))->format("Y-m-d H:i:s e O P T");`,

		// --- modify / relative ---
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("@1700000000"); echo $d->modify("+1 day")->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("@1700000000"); echo $d->modify("-2 hours")->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("@1700000000"); echo $d->modify("+3 months")->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("@1700000000"); echo $d->modify("+1 week")->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-01-02 03:04:05"); echo $d->modify("+1 day +2 hours")->format("Y-m-d H:i:s");`,

		// --- getTimestamp / setTimestamp ---
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-01-02 03:04:05"); echo $d->getTimestamp();`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-01-02 03:04:05"); $d->setTimestamp(1700000000); echo $d->format("Y-m-d H:i:s");`,

		// --- add / sub с DateInterval ---
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("@1700000000"); $d->add(new DateInterval("P1D")); echo $d->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("@1700000000"); $d->sub(new DateInterval("P1D")); echo $d->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("@1700000000"); $d->add(new DateInterval("P1Y2M3DT4H5M6S")); echo $d->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("2023-01-31"))->add(new DateInterval("P1M"))->format("Y-m-d");`,

		// --- diff ---
		`<?php date_default_timezone_set("UTC"); $a = new DateTime("2023-03-05 07:08:09"); $b = new DateTime("2023-01-04 03:04:05"); $x = $a->diff($b); echo $x->format("%y %m %d %h %i %s"), " days=", $x->days, " invert=", $x->invert, " ", $x->format("%R%r%a");`,
		`<?php date_default_timezone_set("UTC"); $a = new DateTime("2023-01-01 23:00:00"); $b = new DateTime("2023-01-02 01:00:00"); $x = $a->diff($b); echo $x->format("%y %m %d %h %i %s"), " days=", $x->days, " invert=", $x->invert;`,
		`<?php date_default_timezone_set("UTC"); $a = new DateTime("2023-01-31"); $b = new DateTime("2023-02-28"); $x = $a->diff($b); echo $x->format("%y %m %d"), " days=", $x->days;`,

		// --- DateTimeImmutable: неизменяемость ---
		`<?php date_default_timezone_set("UTC"); $d = new DateTimeImmutable("2023-01-02 03:04:05"); $e = $d->modify("+1 day"); echo $d->format("Y-m-d H:i:s"), "|", $e->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTimeImmutable("@1700000000"); $e = $d->add(new DateInterval("P1D")); echo $d->format("Y-m-d"), "|", $e->format("Y-m-d");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTimeImmutable("2023-01-02"); $e = $d->setTimestamp(0); echo $d->format("Y-m-d"), "|", $e->format("Y-m-d");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTimeImmutable("2023-01-02"); $e = $d->setDate(2020, 5, 6); echo $d->format("Y-m-d"), "|", $e->format("Y-m-d");`,

		// --- DateTimeZone ---
		`<?php date_default_timezone_set("UTC"); $z = new DateTimeZone("UTC"); echo $z->getName();`,
		`<?php date_default_timezone_set("UTC"); $z = new DateTimeZone("Europe/Moscow"); echo $z->getName(), "|", $z->getOffset(new DateTime("@0"));`,
		`<?php date_default_timezone_set("UTC"); $z = new DateTimeZone("+02:00"); echo $z->getName(), "|", $z->getOffset(new DateTime("@0"));`,
		`<?php echo DateTimeZone::UTC, "|", DateTimeZone::ALL;`,

		// --- getTimezone / setTimezone / setDate / setTime ---
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-01-02 03:04:05"); echo $d->getTimezone()->getName();`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("@1700000000"); $d->setTimezone(new DateTimeZone("Europe/Moscow")); echo $d->format("Y-m-d H:i:s e");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-01-02 03:04:05"); $d->setDate(2020, 5, 6); echo $d->format("Y-m-d H:i:s");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-01-02 03:04:05"); $d->setTime(1, 2, 3); echo $d->format("Y-m-d H:i:s");`,

		// --- DateInterval: format и свойства ---
		`<?php $i = new DateInterval("P1Y2M3DT4H5M6S"); echo $i->format("%y %m %d %h %i %s");`,
		`<?php $i = new DateInterval("P1Y2M3DT4H5M6S"); echo $i->y, "|", $i->m, "|", $i->d, "|", $i->h, "|", $i->i, "|", $i->s;`,
		`<?php $i = new DateInterval("P2W"); echo $i->d, "|", $i->format("%d");`,
		`<?php var_dump((new DateInterval("P1D"))->days);`,

		// --- DatePeriod: обход ---
		`<?php date_default_timezone_set("UTC"); $p = new DatePeriod(new DateTime("2023-01-01"), new DateInterval("P1D"), new DateTime("2023-01-04")); foreach ($p as $k => $v) { echo $k, "=>", $v->format("Y-m-d"), " "; }`,
		`<?php date_default_timezone_set("UTC"); $p = new DatePeriod(new DateTime("2023-01-01"), new DateInterval("P1D"), 3); foreach ($p as $v) { echo $v->format("Y-m-d"), " "; }`,
		`<?php date_default_timezone_set("UTC"); $p = new DatePeriod(new DateTime("2023-01-01"), new DateInterval("P1D"), new DateTime("2023-01-04")); echo get_class($p);`,

		// --- instanceof, get_class, константы ---
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("now"); var_dump($d instanceof DateTime, $d instanceof DateTimeImmutable, $d instanceof DateTimeInterface);`,
		`<?php $d = new DateTimeImmutable("now"); var_dump($d instanceof DateTimeInterface, $d instanceof DateTime);`,
		`<?php $z = new DateTimeZone("UTC"); var_dump($z instanceof DateTimeZone, $z instanceof DateTimeInterface);`,
		`<?php echo DateTime::ATOM, "|", DateTimeInterface::ATOM, "|", DateTime::W3C;`,
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("@0"))->format(DateTime::ATOM);`,

		// --- конструктор с DateTimezone вторым аргументом ---
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("2023-06-01 12:00:00", new DateTimeZone("Europe/Moscow")))->format("Y-m-d H:i:s e O P");`,
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("2023-06-01 12:00:00", new DateTimeZone("Europe/Moscow")))->getTimestamp();`,

		// --- format U/e и цепочки ---
		`<?php date_default_timezone_set("UTC"); echo (new DateTime("@1700000000"))->format("U");`,
		`<?php date_default_timezone_set("Europe/Moscow"); $d = new DateTime("@1700000000"); echo $d->format("Y-m-d H:i:s e");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-01-02 03:04:05"); $d->modify("+1 day")->modify("+1 hour"); echo $d->format("Y-m-d H:i:s");`,

		// --- DateInterval: invert через diff и add ---
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-03-05"); $x = $d->diff(new DateTime("2023-01-01")); echo $x->invert, "|", $x->days;`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-03-05"); $d->add($d->diff(new DateTime("2023-01-01"))); echo $d->format("Y-m-d");`,
		`<?php date_default_timezone_set("UTC"); $d = new DateTime("2023-03-05"); $d->sub($d->diff(new DateTime("2023-04-10"))); echo $d->format("Y-m-d");`,
	}

	for _, src0 := range cases {
		src := src0
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			t.Logf("php error for %q: %v", src, werr)
			continue
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q: run error: %v", src, err)
			continue
		}
		if got != string(want) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}

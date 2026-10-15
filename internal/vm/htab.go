package vm

import "hash/maphash"

// Открытая адресная таблица mixed-массива — замена пары Go-map
// (map[string]int32 + map[int64]int32) из перф-отчёта perf2 (S1).
//
// Раскладка:
//   - buckets []hbucket — степень двойки; в бакете 32-битный отпечаток хеша
//     (fp) и индекс entries+1 (idx): 0 — бакет свободен (make сразу даёт
//     «пустую» таблицу без заливки), bucketTomb (-1) — удалённый (пробирование
//     продолжается, при вставке tombstone переиспользуется как слот). Бакет —
//     8 байт, т.е. 8 бакетов на кэш-линию: промах ищет слот, не трогая
//     entries (раньше каждый шаг пробирования читал KV — случайный промах
//     кэша на 72-байтовой записи), и только совпадение fp ведёт в entries;
//   - полный хеш и ключ лежат в KV (entries[i].hash / .key); после fp читается
//     вся запись — сравнение хеша (страховка от коллизии fp) и ключа;
//   - пробирование линейное, рост — удвоение при загрузке (живые+tomb) > 0.75,
//     при перевесе tombstone таблица перестраивается в ту же ёмкость;
//   - удаление не сжимает entries (tombstone в KV, live=false) — как и раньше.
//
// Почему это быстрее Go-map на профиле 03_arr_assoc (40–44% CPU, mapassign/
// matchH2): один проход на запись (в Go-map было два: Get + assign), плотные
// бакеты вместо бакет-массивов map, отсутствие инкрементального роста (одна
// перестройка с линейным проходом по срезам) и, главное, Unshare/COW
// становится memcpy пары срезов вместо переобхода 150k строк (perf2: 12.5%
// CPU и ~30–40 мс на итерацию 03).
//
// Порядок элементов задаётся только срезами entries (итераторы, pos, Keys,
// Values, foreach), порядок бакетов ненаблюдаем.
const (
	bucketTomb int32 = -1 // удалённый: пробирование продолжается
	bucketFree int32 = 0  // свободный: пробирование останавливается
	htMinSize        = 8
	// htMaxSize — предел ёмкости таблицы: индексы и used — int32, удвоение
	// 2^30 больше не делаем (недостижимо: нужно >1 млрд живых элементов).
	htMaxSize = 1 << 30
)

// hbucket — слот таблицы: отпечаток хеша (старшие 32 бита) + индекс entries+1.
type hbucket struct {
	fp  uint32
	idx int32
}

// htSeed — seed строкового хеша (maphash: тот же алгоритм, что у Go-map).
// Значение seed ненаблюдаемо: порядок обхода массивов задают entries.
var htSeed = maphash.MakeSeed()

// hashKeyStr — 32-битный хеш строкового ключа. Полного 64-битного хеша не
// нужно: коллизии 32 бит разруливает сравнение самого ключа, а 32 бита
// экономят 8 байт на каждую KV (72 -> 64 Б) и позволяют держать отпечаток
// хеша в бакете.
func hashKeyStr(s string) uint32 { return uint32(maphash.String(htSeed, s)) }

// hashKeyInt — хеш int-ключа: splitmix64 (хорошо перемешивает младшие биты,
// по которым берётся маска бакета).
func hashKeyInt(i int64) uint32 {
	x := uint64(i) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return uint32(x ^ (x >> 31))
}

// htSizeFor — минимальная ёмкость (степень двойки), вмещающая n живых
// элементов при загрузке < 0.75.
func htSizeFor(n int) int {
	nb := htMinSize
	for nb < htMaxSize && int64(nb)*3 < int64(n)*4 {
		nb *= 2
	}
	return nb
}

// htFindStr ищет строковый ключ s с хешем h. e >= 0 — ключ найден в entries[e]
// (b — его бакет); e < 0 — ключа нет, b — бакет для вставки нового элемента
// (первый tombstone или свободный по цепочке). Требует len(buckets) > 0.
func (a *Array) htFindStr(s string, h uint32) (e, b int32) {
	buckets := a.buckets
	mask := int32(len(buckets) - 1)
	i := int32(h) & mask
	ins := int32(-1)
	for {
		x := buckets[i]
		if x.idx == bucketFree {
			if ins < 0 {
				ins = i
			}
			return -1, ins
		}
		if x.idx < 0 {
			if ins < 0 {
				ins = i
			}
		} else if x.fp == h {
			// Хеш совпал: проверяем сам ключ (он же решает коллизии 32 бит).
			if kv := &a.entries[x.idx-1]; kv.key.str && kv.key.s == s {
				return x.idx - 1, i
			}
		}
		i = (i + 1) & mask
	}
}

// htFindInt — то же для int-ключа.
func (a *Array) htFindInt(k int64, h uint32) (e, b int32) {
	buckets := a.buckets
	mask := int32(len(buckets) - 1)
	i := int32(h) & mask
	ins := int32(-1)
	for {
		x := buckets[i]
		if x.idx == bucketFree {
			if ins < 0 {
				ins = i
			}
			return -1, ins
		}
		if x.idx < 0 {
			if ins < 0 {
				ins = i
			}
		} else if x.fp == h {
			if kv := &a.entries[x.idx-1]; !kv.key.str && kv.key.i == k {
				return x.idx - 1, i
			}
		}
		i = (i + 1) & mask
	}
}

// idxOfStr — индекс entries живого элемента со строковым ключом s (для
// состояний итераторов: mode/cell/aid).
func (a *Array) idxOfStr(s string) (int32, bool) {
	if len(a.buckets) == 0 {
		return -1, false
	}
	e, _ := a.htFindStr(s, hashKeyStr(s))
	return e, e >= 0
}

// htNeedGrow — перед вставкой нового ключа таблице нужен рост/перестройка:
// занято (живые+tombstone) больше 3/4 ёмкости.
func (a *Array) htNeedGrow() bool {
	return (a.used+1)*4 > int32(len(a.buckets))*3
}

// htGrow — перестройка таблицы перед вставкой: при плотной загрузке живых —
// удвоение, иначе та же ёмкость (заодно вычищаются tombstone).
func (a *Array) htGrow() {
	nb := len(a.buckets)
	if int64(a.nLive)*8 >= int64(nb)*6 && nb < htMaxSize {
		nb *= 2
	}
	a.htResize(nb)
}

// htResize строит таблицу ёмкости nb (степень двойки) по живым entries.
// Вызывать только когда загрузка после перестройки < 1 (иначе пробирование
// зациклится); htSizeFor/htGrow это обеспечивают.
func (a *Array) htResize(nb int) {
	buckets := make([]hbucket, nb) // нули = свободные бакеты
	mask := int32(nb - 1)
	used := int32(0)
	for i := range a.entries {
		kv := &a.entries[i]
		if !kv.live {
			continue
		}
		j := int32(kv.hash) & mask
		for buckets[j].idx != bucketFree {
			j = (j + 1) & mask
		}
		buckets[j] = hbucket{fp: kv.hash, idx: int32(i) + 1}
		used++
	}
	a.buckets = buckets
	a.used = used
}

// htEnsure — таблица существует (ленивое создание при первой вставке).
func (a *Array) htEnsure() {
	if len(a.buckets) == 0 {
		a.htResize(htMinSize)
	}
}

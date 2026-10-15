package php

// zsort.go — точный порт Zend zend_sort (Zend/zend_sort.c, PHP 8.3).
//
// Порядок сравнений наблюдаем: компараторы пользователя и предупреждения о
// преобразовании типов (SORT_STRING по массивам/объектам) появляются ровно в
// том порядке, в котором их делает PHP. Поэтому вместо sort.SliceStable
// используется этот порт: гибрид сортировки вставками (<= 16 элементов) и
// quicksort из libc++ с медианой из 3/5.
//
// cmp(i, j) возвращает <0/0/>0; swp(i, j) меняет элементы местами. Стабильность
// обеспечена сравнением исходных позиций при равенстве (Zend: Z_EXTRA и
// stable_sort_fallback).

// zendSortSlices сортирует n элементов, сохраняя стабильность по исходному
// порядку (как php_array_* через RETURN_STABLE_SORT).
func zendSortSlices(n int, cmp func(i, j int) int, swp func(i, j int)) {
	if n < 2 {
		return
	}
	orig := make([]int, n)
	for i := range orig {
		orig[i] = i
	}
	c := func(i, j int) int {
		if r := cmp(i, j); r != 0 {
			return r
		}
		return orig[i] - orig[j]
	}
	sw := func(i, j int) {
		swp(i, j)
		orig[i], orig[j] = orig[j], orig[i]
	}
	zendSortRange(0, n, c, sw)
}

// zendSortRange — итеративная часть zend_sort (база — индекс, шаг — 1).
func zendSortRange(start, nmemb int, cmp func(i, j int) int, swp func(i, j int)) {
	for {
		if nmemb <= 16 {
			zendInsertSort(start, nmemb, cmp, swp)
			return
		}
		end := start + nmemb
		offset := nmemb >> 1
		pivot := start + offset
		if nmemb>>10 != 0 {
			delta := offset >> 1
			zendSort5(start, start+delta, pivot, pivot+delta, end-1, cmp, swp)
		} else {
			zendSort3(start, pivot, end-1, cmp, swp)
		}
		swp(start+1, pivot)
		pivot = start + 1
		i := pivot + 1
		j := end - 1
		// Метки done из C: выходим из обоих циклов.
		done := false
		for !done {
			for cmp(pivot, i) > 0 {
				i++
				if i == j {
					done = true
					break
				}
			}
			if done {
				break
			}
			j--
			if j == i {
				break
			}
			for cmp(j, pivot) > 0 {
				j--
				if j == i {
					done = true
					break
				}
			}
			if done {
				break
			}
			swp(i, j)
			i++
			if i == j {
				break
			}
		}
		swp(pivot, i-1)
		if (i-1)-start < end-i {
			zendSortRange(start, (i-start)-1, cmp, swp)
			start = i
			nmemb = end - i
		} else {
			zendSortRange(i, end-i, cmp, swp)
			nmemb = (i - start) - 1
		}
	}
}

func zendSort2(a, b int, cmp func(i, j int) int, swp func(i, j int)) {
	if cmp(a, b) > 0 {
		swp(a, b)
	}
}

func zendSort3(a, b, c int, cmp func(i, j int) int, swp func(i, j int)) {
	if !(cmp(a, b) > 0) {
		if !(cmp(b, c) > 0) {
			return
		}
		swp(b, c)
		if cmp(a, b) > 0 {
			swp(a, b)
		}
		return
	}
	if !(cmp(c, b) > 0) {
		swp(a, c)
		return
	}
	swp(a, b)
	if cmp(b, c) > 0 {
		swp(b, c)
	}
}

func zendSort4(a, b, c, d int, cmp func(i, j int) int, swp func(i, j int)) {
	zendSort3(a, b, c, cmp, swp)
	if cmp(c, d) > 0 {
		swp(c, d)
		if cmp(b, c) > 0 {
			swp(b, c)
			if cmp(a, b) > 0 {
				swp(a, b)
			}
		}
	}
}

func zendSort5(a, b, c, d, e int, cmp func(i, j int) int, swp func(i, j int)) {
	zendSort4(a, b, c, d, cmp, swp)
	if cmp(d, e) > 0 {
		swp(d, e)
		if cmp(c, d) > 0 {
			swp(c, d)
			if cmp(b, c) > 0 {
				swp(b, c)
				if cmp(a, b) > 0 {
					swp(a, b)
				}
			}
		}
	}
}

// zendInsertSort — порт zend_insert_sort.
func zendInsertSort(base, nmemb int, cmp func(i, j int) int, swp func(i, j int)) {
	switch nmemb {
	case 0, 1:
		return
	case 2:
		zendSort2(base, base+1, cmp, swp)
		return
	case 3:
		zendSort3(base, base+1, base+2, cmp, swp)
		return
	case 4:
		zendSort4(base, base+1, base+2, base+3, cmp, swp)
		return
	case 5:
		zendSort5(base, base+1, base+2, base+3, base+4, cmp, swp)
		return
	}
	start := base
	end := start + nmemb
	sentry := start + 6
	for i := start + 1; i < sentry; i++ {
		j := i - 1
		if !(cmp(j, i) > 0) {
			continue
		}
		for j != start {
			j--
			if !(cmp(j, i) > 0) {
				j++
				break
			}
		}
		for k := i; k > j; k-- {
			swp(k, k-1)
		}
	}
	for i := sentry; i < end; i++ {
		j := i - 1
		if !(cmp(j, i) > 0) {
			continue
		}
		for {
			j -= 2
			if !(cmp(j, i) > 0) {
				j++
				if !(cmp(j, i) > 0) {
					j++
				}
				break
			}
			if j == start {
				break
			}
			if j == start+1 {
				j--
				if cmp(i, j) > 0 {
					j++
				}
				break
			}
		}
		for k := i; k > j; k-- {
			swp(k, k-1)
		}
	}
}

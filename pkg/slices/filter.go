package slices

func Filter[S ~[]E, F ~func(int, E) bool, E any](s S, fn F) S {
	res := make(S, 0, len(s))

	for i, e := range s {
		if fn(i, e) {
			res = append(res, e)
		}
	}

	return res
}

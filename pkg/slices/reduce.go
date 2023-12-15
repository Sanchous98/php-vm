package slices

func Reduce[E, R any, S ~[]E, F ~func(int, E, R) R](s S, reduce F, init R) R {
	for i, e := range s {
		init = reduce(i, e, init)
	}
	return init
}

//go:build dumpbc

package vm

// SetDumpPreAllocHook installs an SSA hook that dumps the pre-allocation
// bytecode of every chunk.
func SetDumpPreAllocHook() {
	optimizeSSAHook = func(g *ssaGraph) {
		println("--- pre-alloc ---")
		g.DumpPreAlloc()
	}
}

// DumpPreAlloc prints the input bytecode of the graph.
func (g *ssaGraph) DumpPreAlloc() {
	for i, in := range g.code {
		println(i, in.Op.String(), int(in.A), int(in.B), int(in.C))
	}
}

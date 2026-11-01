package xp

import "testing"

// BenchmarkBuiltins watches the cost of reaching a builtin. Dispatch used to be
// one enormous switch; the table that replaced it is far easier to read, and it
// must not buy that readability with a hidden allocation on every call. These
// expressions are chains of builtins over one small node, so an iteration's
// allocations are almost entirely the calls themselves: if the argument vector
// or its arguments start escaping to the heap again, this benchmark shows it
// and per-call ns rises with them.
//
//	go test -run '^$' -bench Builtins -benchmem -cpu 1 internal/xp
var builtinExprs = []string{
	`boolean(normalize-space(string(*)))`,
	`matches(local-name(*), 'A') and contains(name(), 'ram')`,
	`not(empty(*)) and string-length(local-name(*)) > 0`,
	`exists(*[position() = last()]) and count(*) = 3`,
	`sum(number(*), 1) + round(2.5) = ceiling(4)`,
	`string-join(tokenize(name(), ':'), '-') = 'ram-A'`,
}

func BenchmarkBuiltins(b *testing.B) {
	d, err := ParseXML(tdoc)
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	exprs := make([]Expr, 0, len(builtinExprs))
	for _, src := range builtinExprs {
		e, err := Compile(src, nil)
		if err != nil {
			b.Fatalf("compile %s: %v", src, err)
		}
		exprs = append(exprs, e)
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, e := range exprs {
			if _, err := e.Eval(d, d.Root); err != nil {
				b.Fatalf("eval: %v", err)
			}
		}
	}
}

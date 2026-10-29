package account

import (
	"strings"
	"testing"
)

// FuzzNormalizeSymbols: a typed code is read as n symbols of the code
// alphabet or refused, and a code read once reads the same again.
func FuzzNormalizeSymbols(f *testing.F) {
	for _, s := range []string{"7KQ2M-XR4WD", "7kq2m xr4wd", "ABCDE-FGHJK-MNPQR-STVWX", "oooooIIIII", "", "ü", strings.Repeat("-", 100)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, typed string) {
		for _, n := range []int{codeLen, linkCodeLen} {
			code, ok := normalizeSymbols(typed, n)
			if !ok {
				continue
			}
			if len(code) != n || strings.Trim(code, codeAlphabet) != "" {
				t.Fatalf("normalizeSymbols(%q, %d) = %q", typed, n, code)
			}
			if again, ok := normalizeSymbols(groupCode(code), n); !ok || again != code {
				t.Fatalf("%q read as %q, then %q", typed, code, again)
			}
		}
	})
}

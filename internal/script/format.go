package script

import (
	"fmt"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// strFormat is string.format with Lua 5.1's conversions only:
// %[flags][width][.precision]conv with flags from "-+ #0", width and
// precision of at most two digits, and conv one of c d i o u x X e E f g G
// q s, plus %%; %s takes strings and numbers. gopher-lua hands the format string to Go's fmt, where
// %[n]d reuses an argument any number of times and %v or %p print Go
// internals; this parses the spec itself and formats one argument at a time,
// checking the length before every append.
func strFormat(L *lua.LState) int {
	format := L.CheckString(1)
	arg := 1
	var b strings.Builder
	add := func(s string) {
		if b.Len()+len(s) > MaxStringBytes {
			L.RaiseError("string.format result larger than %d bytes", MaxStringBytes)
		}
		b.WriteString(s)
	}
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			add(string(c))
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			add("%")
			continue
		}
		start := i
		for i < len(format) && strings.IndexByte("-+ #0", format[i]) >= 0 {
			i++
		}
		flags := format[start:i]
		var width string
		width, i = digits(format, i)
		precision := ""
		if i < len(format) && format[i] == '.' {
			precision, i = digits(format, i+1)
			precision = "." + precision
		}
		if len(flags) > 5 || len(width) > 2 || len(precision) > 3 {
			L.RaiseError("invalid format (width or precision too long)")
		}
		if i >= len(format) {
			L.RaiseError("invalid option '%%' to 'format'")
		}
		conv := format[i]
		arg++
		spec := "%" + flags + width + precision
		switch conv {
		case 'd', 'i':
			add(fmt.Sprintf(spec+"d", int64(L.CheckNumber(arg))))
		case 'u':
			add(fmt.Sprintf(spec+"d", uint64(int64(L.CheckNumber(arg)))))
		case 'o', 'x', 'X':
			add(fmt.Sprintf(spec+string(conv), uint64(int64(L.CheckNumber(arg)))))
		case 'c':
			add(string([]byte{byte(int64(L.CheckNumber(arg)))}))
		case 'e', 'E', 'f', 'g', 'G':
			if precision == "" {
				spec += ".6" // C's default; Go's would print the shortest form
			}
			add(fmt.Sprintf(spec+string(conv), float64(L.CheckNumber(arg))))
		case 's':
			// Strings and numbers only, as in Lua 5.1: a __tostring here
			// could wait in ui.dialog while this function holds a partial
			// result the state walk cannot see.
			s := L.CheckString(arg)
			if width == "" && precision == "" {
				add(s)
			} else {
				add(fmt.Sprintf(spec+"s", s))
			}
		case 'q':
			add(quote(L.CheckString(arg)))
		default:
			L.RaiseError("invalid option '%%%c' to 'format'", conv)
		}
	}
	L.Push(lua.LString(b.String()))
	return 1
}

// digits returns the run of ASCII digits at format[i:] and the index after it.
func digits(format string, i int) (string, int) {
	start := i
	for i < len(format) && format[i] >= '0' && format[i] <= '9' {
		i++
	}
	return format[start:i], i
}

// quote is Lua 5.1's %q: a double-quoted string that reads back as itself.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString("\\\n")
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\000`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

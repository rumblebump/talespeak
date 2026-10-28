package script

import (
	"strings"
	"testing"
)

// math.random draws from a source no script seeds (T-0033).
func TestMathRandom(t *testing.T) {
	out, err := hook(t, `
for i = 1, 200 do
  local a, b, c = math.random(), math.random(3), math.random(-2, 2)
  if a < 0 or a >= 1 then error("random() = " .. a) end
  if b < 1 or b > 3 or b ~= math.floor(b) then error("random(3) = " .. b) end
  if c < -2 or c > 2 then error("random(-2, 2) = " .. c) end
end
say(tostring(math.random(5, 5)) .. " " .. tostring(math.random(2.9)) .. " " .. tostring(math.randomseed))`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(out, "|"); got != "say:5 1 nil" && got != "say:5 2 nil" {
		t.Errorf("got %s", got)
	}
}

func TestMathRandomErrors(t *testing.T) {
	for _, tt := range []struct{ body, want string }{
		{`math.random(0)`, "interval is empty"},
		{`math.random(3, 1)`, "interval is empty"},
		{`math.random(1, 2, 3)`, "wrong number of arguments"},
		{`math.random("x")`, "number expected"},
		{`math.random(1e300)`, "out of range"},
		{`math.random(-1e300, 1)`, "out of range"},
		{`math.random(0/0)`, "out of range"},
		{`math.randomseed(42)`, "attempt to call"},
	} {
		_, err := hook(t, tt.body)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.body, err, tt.want)
		}
	}
}

// The bounds of math.random's arguments are allowed; past them is an
// error.
func TestMathRandomBounds(t *testing.T) {
	out, err := hook(t, `
local a = math.random(2^53)
local b = math.random(-2^53, 2^53)
local c = math.random(-2^53, -2^53)
say(tostring(a >= 1 and a <= 2^53) .. " " .. tostring(b >= -2^53 and b <= 2^53) .. " " .. tostring(c == -2^53))`)
	if err != nil || strings.Join(out, "|") != "say:true true true" {
		t.Errorf("bounds: %v, %v", out, err)
	}
	for _, body := range []string{`math.random(2^53 + 2)`, `math.random(-2^53 - 2, 0)`, `math.random(1/0)`} {
		if _, err := hook(t, body); err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("%s: %v", body, err)
		}
	}
}

// No script changes math.random for another: the math table is the
// sandbox's, read-only; and two runtimes (two worlds) draw different
// numbers, so neither follows a seed the other set.
func TestMathRandomShared(t *testing.T) {
	if _, err := hook(t, `math.random = function() return 4 end`); err == nil {
		t.Error("a script replaced math.random")
	}
	if _, err := hook(t, `rawset(math, "random", function() return 4 end)`); err == nil {
		t.Error("a script replaced math.random with rawset")
	}
	draw := func() string {
		out, err := hook(t, `local t = {} for i = 1, 8 do t[i] = tostring(math.random(1e9)) end say(table.concat(t, ","))`)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, "|")
	}
	if a, b := draw(), draw(); a == b {
		t.Errorf("two runtimes drew the same numbers: %s", a)
	}
}

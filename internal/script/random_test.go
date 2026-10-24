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

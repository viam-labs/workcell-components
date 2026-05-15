package workcellcomponents

import "fmt"

// asFloat coerces an interface{} from a DoCommand args map into a
// float64, handling the typical JSON numeric shapes that arrive at the
// gRPC boundary. Returns 0 for any non-numeric input.
func asFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// Color is RGB in 0–255 plus optional opacity (0–1, default 1). Both
// pallet and pick-station accept a Color in their config and via the
// `set_color` DoCommand; consumers (3D-viewer producers, debug logs)
// read it through `get_color` / `get_attributes`.
type Color struct {
	R int     `json:"r"`
	G int     `json:"g"`
	B int     `json:"b"`
	A float64 `json:"opacity,omitempty"` // 0..1, 0 means "use default" (1.0)
}

// asColor parses a DoCommand color argument. Accepts the {r,g,b,opacity}
// map shape produced by encoding/json on a Color. Returns the parsed
// color and a "present" flag — the caller decides what to do when the
// argument is missing (typically leave the current color in place).
func asColor(v interface{}) (Color, bool) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return Color{}, false
	}
	c := Color{
		R: int(asFloat(m["r"])),
		G: int(asFloat(m["g"])),
		B: int(asFloat(m["b"])),
		A: asFloat(m["opacity"]),
	}
	return c, true
}

func (c Color) toMap() map[string]interface{} {
	out := map[string]interface{}{"r": c.R, "g": c.G, "b": c.B}
	if c.A > 0 {
		out["opacity"] = c.A
	}
	return out
}

// effectiveOpacity returns the color's opacity if set, else 1.0.
func (c Color) effectiveOpacity() float64 {
	if c.A <= 0 || c.A > 1 {
		return 1
	}
	return c.A
}

// validateColorChannel rejects out-of-range RGB values. RGB is 0..255.
// Returning an error from set_color rather than silently clamping lets
// the caller correct typos rather than wonder why their color "looks
// off."
func validateColorChannel(name string, v int) error {
	if v < 0 || v > 255 {
		return fmt.Errorf("%s must be 0..255, got %d", name, v)
	}
	return nil
}

func validateColor(c Color) error {
	if err := validateColorChannel("color.r", c.R); err != nil {
		return err
	}
	if err := validateColorChannel("color.g", c.G); err != nil {
		return err
	}
	if err := validateColorChannel("color.b", c.B); err != nil {
		return err
	}
	if c.A < 0 || c.A > 1 {
		return fmt.Errorf("color.opacity must be 0..1, got %v", c.A)
	}
	return nil
}

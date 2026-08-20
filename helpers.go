package workcellcomponents

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang/geo/r3"
)

// asFloat coerces an interface{} from a DoCommand args map into a
// float64, handling the typical JSON numeric shapes that arrive at the
// gRPC boundary. Returns 0 for any non-numeric input.
// sensorReadTimeout bounds a paired sensor's Readings RPC from inside
// a visual builder, so a wedged sensor cannot stall a component.
const sensorReadTimeout = 500 * time.Millisecond

// errNotABoxDetect / errNotATrayDock: the paired sensor answered, but
// its readings lack the keys the pairing relies on.
var (
	errNotABoxDetect = errors.New(
		"paired sensor readings lack box_present; not a box-detect?")
	errNotATrayDock = errors.New(
		"paired sensor readings lack tray_present; not a tray-dock?")
)

// cardboardColor is the shared box-brown used by the infeed box default
// and the tray exchange's load silhouette.
var cardboardColor = Color{R: 176, G: 136, B: 80, A: 1}

// isTruthy reads a DoCommand flag that may arrive as a bool, a number,
// or a string depending on how the caller's SDK encoded it.
func isTruthy(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case int:
		return t != 0
	case string:
		return t == "true" || t == "True" || t == "1"
	default:
		return false
	}
}

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

// persistHint is the standard "your live edit isn't durable" warning
// embedded in every set_* response. The cell config is the source of
// truth — a reconfigure (or viam-server restart) reverts in-memory
// mutations.
const persistHint = "live until reconfigure; persist by editing the cell config"

// withPersistHint annotates a set_* response with the standard
// persistence warning. Pure pass-through on the error path.
func withPersistHint(resp map[string]interface{}, err error) (map[string]interface{}, error) {
	if err != nil {
		return resp, err
	}
	if resp == nil {
		resp = map[string]interface{}{}
	}
	resp["persisted"] = false
	resp["hint"] = persistHint
	return resp, nil
}

// safetyHeightArg pulls a safety_height_mm value from the verb's
// argument. The verb value can be `true` (use default), a number
// (interpret as the height directly), or a `{"safety_height_mm": h}`
// object. Anything else falls back to the default.
func safetyHeightArg(verbValue interface{}, defaultMM float64) float64 {
	switch v := verbValue.(type) {
	case bool:
		return defaultMM
	case float64:
		if v > 0 {
			return v
		}
	case map[string]interface{}:
		if h := asFloat(v["safety_height_mm"]); h > 0 {
			return h
		}
	}
	return defaultMM
}

// applyVisualOptions reads show_axes / visible / opacity from a
// set_attributes payload map into the supplied VisualOptions struct.
// Missing fields are no-ops; partial updates supported.
func applyVisualOptions(opts *VisualOptions, m map[string]interface{}) {
	if v, ok := m["show_axes"].(bool); ok {
		opts.ShowAxes = v
	}
	if v, ok := m["visible"].(bool); ok {
		b := v
		opts.Visible = &b
	}
	if v, ok := m["opacity"]; ok {
		f := asFloat(v)
		if f >= 0 && f <= 1 {
			opts.Opacity = &f
		}
	}
}

// mergeVisualOptions appends the visual-options keys to an
// attributes-shaped map. Visible / Opacity render as concrete bool /
// number values (defaults filled in) so consumers don't have to handle
// the unset case.
func mergeVisualOptions(out map[string]interface{}, opts VisualOptions) {
	out["show_axes"] = opts.ShowAxes
	visible := true
	if opts.Visible != nil {
		visible = *opts.Visible
	}
	out["visible"] = visible
	opacity := 1.0
	if opts.Opacity != nil {
		opacity = *opts.Opacity
	}
	out["opacity"] = opacity
}

// defaultLen returns v if positive, else fallback. Used by affordance
// constructors that want config to override a hard-coded default.
func defaultLen(v, fallback float64) float64 {
	if v > 0 {
		return v
	}
	return fallback
}

// clamp01 clamps a value into [0, 1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// r3vec is a tiny constructor for r3.Vector. Convenience.
func r3vec(x, y, z float64) r3.Vector {
	return r3.Vector{X: x, Y: y, Z: z}
}

// coerceStringSlice normalizes a DoCommand argument that should be a
// list of strings. May arrive as []string (in-process Go) or []any
// (gRPC structpb). Anything else returns nil — caller treats as
// "unset / no change."
func coerceStringSlice(v interface{}) []string {
	switch tv := v.(type) {
	case []string:
		return tv
	case []interface{}:
		out := make([]string, 0, len(tv))
		for _, x := range tv {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

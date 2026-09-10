package workcellcomponents

import (
	"encoding/json"
	"fmt"

	"github.com/viam-labs/viam-viz-helpers-go"
)

// The `get_visuals` DoCommand verb that every workcell component
// implements returns a flat list of wire-format visual entries. The
// `workcell-scene` service translates each entry into a typed
// visuals.Visual (Box / Sphere / Capsule / Arrow / Mesh) for the
// viam-viz-helpers-go library, which handles wire encoding + renderer
// quirks.
//
// Wire format (snake_case, JSON-compatible — works in-process and
// across gRPC structpb):
//
//	{
//	  "type": "box" | "sphere" | "capsule" | "arrow" | "mesh",
//	  "label": "pallet/slat-0",
//	  "parent_frame": "world",
//	  "pose": {"x", "y", "z", "o_x", "o_y", "o_z", "theta"},
//	  "dims_mm": {"x", "y", "z"},        // box only
//	  "radius_mm": 12.0,                 // sphere / capsule / arrow
//	  "length_mm": 1200.0,               // capsule / arrow
//	  "mesh_path": "path/to.ply",        // mesh only
//	  "color": {"r", "g", "b", "opacity"},
//	  "show_axes_helper": false,
//	  "invisible": false,
//	  "animation": {"mode", "period_s", "axis", "amplitude_mm", ...}
//	}
//
// The pose keys use `o_x`/`o_y`/`o_z` (snake_case) so they match the
// existing `get_pose` / `get_visual_pose` shape on pallet / pick-
// station. The library's wirePose uses `ox`/`oy`/`oz` — visualWire is
// our adapter.

// visualWire is the per-entry wire shape (matches the get_visuals
// JSON exactly).
type visualWire struct {
	Type           string                 `json:"type"`
	Label          string                 `json:"label"`
	ParentFrame    string                 `json:"parent_frame,omitempty"`
	Pose           *visualPoseWire        `json:"pose,omitempty"`
	DimsMM         *visualDimsWire        `json:"dims_mm,omitempty"`
	RadiusMM       float64                `json:"radius_mm,omitempty"`
	LengthMM       float64                `json:"length_mm,omitempty"`
	MeshPath       string                 `json:"mesh_path,omitempty"`
	Color          *visualColorWire       `json:"color,omitempty"`
	Opacity        *float64               `json:"opacity,omitempty"`
	ShowAxesHelper bool                   `json:"show_axes_helper,omitempty"`
	Invisible      bool                   `json:"invisible,omitempty"`
	Animation      map[string]interface{} `json:"animation,omitempty"`
}

type visualPoseWire struct {
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
	Z     float64 `json:"z,omitempty"`
	OX    float64 `json:"o_x,omitempty"`
	OY    float64 `json:"o_y,omitempty"`
	OZ    float64 `json:"o_z,omitempty"`
	Theta float64 `json:"theta,omitempty"`
}

type visualDimsWire struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type visualColorWire struct {
	R       int     `json:"r"`
	G       int     `json:"g"`
	B       int     `json:"b"`
	Opacity float64 `json:"opacity,omitempty"`
}

// wireToVisual parses one entry from a `get_visuals` response into a
// typed visuals.Visual. Returns an error rather than panicking so a
// single malformed entry doesn't kill the whole scene poll.
// wireToVisual decodes one get_visuals entry, honouring any animation
// spec it carries. Kept as-is for callers/tests that want the default.
func wireToVisual(m map[string]interface{}) (visuals.Visual, error) {
	return wireToVisualOpts(m, false)
}

// wireToVisualOpts is wireToVisual with an animation kill-switch.
//
// stripAnimation drops the entry's Animation spec at decode time, so the
// Visual reaches the scene inert and the library's tick loop has nothing to
// dispatch for it. This is the single choke point for every visual from every
// component, which is why the toggle lives here rather than in each builder:
// the workcell-scene service can silence ALL animation without any component
// knowing. Object count is unchanged — use a component's own render_* flags
// for that.
func wireToVisualOpts(m map[string]interface{}, stripAnimation bool) (visuals.Visual, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var w visualWire
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if w.Label == "" {
		return nil, fmt.Errorf("visual missing label")
	}
	if w.Type == "" {
		return nil, fmt.Errorf("visual %q missing type", w.Label)
	}
	pose := w.poseToLib()
	color := w.colorToLib()
	opacity := w.opacityToLib()
	anim := w.animToLib()
	if stripAnimation {
		anim = nil
	}

	switch w.Type {
	case "box":
		if w.DimsMM == nil || w.DimsMM.X <= 0 || w.DimsMM.Y <= 0 || w.DimsMM.Z <= 0 {
			return nil, fmt.Errorf("box %q: dims_mm required and positive", w.Label)
		}
		return &visuals.Box{
			Label:          w.Label,
			Pose:           pose,
			ParentFrame:    w.ParentFrame,
			DimsMM:         visuals.BoxDims{X: w.DimsMM.X, Y: w.DimsMM.Y, Z: w.DimsMM.Z},
			Color:          color,
			Opacity:        opacity,
			ShowAxesHelper: w.ShowAxesHelper,
			Invisible:      w.Invisible,
			Animation:      anim,
		}, nil
	case "sphere":
		if w.RadiusMM <= 0 {
			return nil, fmt.Errorf("sphere %q: radius_mm required and positive", w.Label)
		}
		return &visuals.Sphere{
			Label:          w.Label,
			Pose:           pose,
			ParentFrame:    w.ParentFrame,
			RadiusMM:       w.RadiusMM,
			Color:          color,
			Opacity:        opacity,
			ShowAxesHelper: w.ShowAxesHelper,
			Invisible:      w.Invisible,
			Animation:      anim,
		}, nil
	case "capsule":
		if w.RadiusMM <= 0 || w.LengthMM <= 0 {
			return nil, fmt.Errorf("capsule %q: radius_mm + length_mm required and positive", w.Label)
		}
		return &visuals.Capsule{
			Label:          w.Label,
			Pose:           pose,
			ParentFrame:    w.ParentFrame,
			RadiusMM:       w.RadiusMM,
			LengthMM:       w.LengthMM,
			Color:          color,
			Opacity:        opacity,
			ShowAxesHelper: w.ShowAxesHelper,
			Invisible:      w.Invisible,
			Animation:      anim,
		}, nil
	case "arrow":
		if w.RadiusMM <= 0 || w.LengthMM <= 0 {
			return nil, fmt.Errorf("arrow %q: radius_mm + length_mm required and positive", w.Label)
		}
		return &visuals.Arrow{
			Label:          w.Label,
			Pose:           pose,
			ParentFrame:    w.ParentFrame,
			LengthMM:       w.LengthMM,
			RadiusMM:       w.RadiusMM,
			Color:          color,
			Opacity:        opacity,
			ShowAxesHelper: w.ShowAxesHelper,
			Invisible:      w.Invisible,
			Animation:      anim,
		}, nil
	case "mesh":
		if w.MeshPath == "" {
			return nil, fmt.Errorf("mesh %q: mesh_path required", w.Label)
		}
		return &visuals.Mesh{
			Label:          w.Label,
			Pose:           pose,
			ParentFrame:    w.ParentFrame,
			MeshPath:       w.MeshPath,
			Color:          color,
			Opacity:        opacity,
			ShowAxesHelper: w.ShowAxesHelper,
			Invisible:      w.Invisible,
			Animation:      anim,
		}, nil
	case "frame":
		// A pure transform anchor. Visible=true is deliberate even
		// though we don't *want* to see the anchor: the library's
		// Frame{Visible:false} translates to metadata.invisible=true,
		// which makes the entity show as hidden in the Viam 3D tree
		// AND (on at least some renderer builds) cascades the hidden
		// state to children — the whole component appears hidden by
		// default. Visible=true emits a 1 mm sphere that's
		// imperceptible at workcell scales while keeping the tree
		// entry (and its children) visible. HideAxes still suppresses
		// the axes helper; we draw axes as child arrows separately.
		return &visuals.Frame{
			Label:       w.Label,
			Pose:        pose,
			ParentFrame: w.ParentFrame,
			Visible:     true,
			HideAxes:    !w.ShowAxesHelper,
		}, nil
	}
	return nil, fmt.Errorf("unknown visual type %q (label %q)", w.Type, w.Label)
}

func (w visualWire) poseToLib() visuals.Pose {
	if w.Pose == nil {
		return visuals.IdentityPose()
	}
	return visuals.PoseAt(w.Pose.X, w.Pose.Y, w.Pose.Z, w.Pose.OX, w.Pose.OY, w.Pose.OZ, w.Pose.Theta)
}

func (w visualWire) colorToLib() *visuals.Color {
	if w.Color == nil {
		return nil
	}
	return &visuals.Color{R: w.Color.R, G: w.Color.G, B: w.Color.B}
}

// opacityToLib extracts opacity from the color block (if present) or
// the top-level opacity field. The wire shape carries opacity inside
// the color object to match the existing get_color / set_color
// convention; the top-level opacity is supported as a fallback.
func (w visualWire) opacityToLib() *float64 {
	if w.Color != nil && w.Color.Opacity > 0 && w.Color.Opacity <= 1 {
		op := w.Color.Opacity
		return &op
	}
	if w.Opacity != nil && *w.Opacity > 0 && *w.Opacity <= 1 {
		op := *w.Opacity
		return &op
	}
	return nil
}

// animToLib parses the optional animation block into a typed
// visuals.AnimationSpec. Supports spin, oscillate, swing, pulse,
// orbit, breathe, flicker — the modes useful to workcell visuals.
// Returns nil for missing / unrecognized modes (renderer-side static).
func (w visualWire) animToLib() visuals.AnimationSpec {
	if len(w.Animation) == 0 {
		return nil
	}
	mode, _ := w.Animation["mode"].(string)
	if mode == "" || mode == "none" {
		return nil
	}
	period := asFloat(w.Animation["period_s"])
	axis, _ := w.Animation["axis"].(string)
	amp := asFloat(w.Animation["amplitude_mm"])
	phase := asFloat(w.Animation["phase_offset_s"])
	ampDeg := asFloat(w.Animation["amplitude_deg"])
	dutyCycle := asFloat(w.Animation["duty_cycle"])

	switch mode {
	case "spin":
		return visuals.Spin{PeriodS: period}
	case "swing":
		return visuals.Swing{AmplitudeDeg: ampDeg, PeriodS: period, PhaseOffsetS: phase}
	case "oscillate":
		if axis == "" {
			return nil
		}
		return visuals.Oscillate{Axis: axis, AmplitudeMM: amp, PeriodS: period, PhaseOffsetS: phase}
	case "orbit":
		return visuals.Orbit{RadiusMM: amp, PeriodS: period}
	case "pulse":
		return visuals.Pulse{AmplitudeMM: amp, PeriodS: period, Axis: axis}
	case "breathe":
		amplitude := asFloat(w.Animation["amplitude"])
		return visuals.Breathe{Amplitude: amplitude, PeriodS: period}
	case "flicker":
		return visuals.Flicker{PeriodS: period, DutyCycle: dutyCycle, PhaseOffsetS: phase}
	}
	return nil
}

// visualsAsInterfaceSlice converts a typed []visuals.Visual into the
// []interface{} that visuals.Scene.Add / .Update / .AddOrUpdate
// accept. Cheap; trades a single allocation for type safety at the
// call site.
func visualsAsInterfaceSlice(vs []visuals.Visual) []interface{} {
	out := make([]interface{}, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}

// visualWireToMap round-trips a typed visualWire through encoding/json
// into a generic map so it can be returned via DoCommand. The JSON
// tags on visualWire / visualPoseWire / visualDimsWire define the
// wire-format keys.
func visualWireToMap(v visualWire) (map[string]interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal visualWire: %w", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal visualWire: %w", err)
	}
	return out, nil
}

// coerceWireVisualsSlice normalizes the `visuals` field of a
// get_visuals response. The slice may arrive as []map[string]any
// (in-process Go) or []any (gRPC via structpb).
func coerceWireVisualsSlice(v interface{}) []map[string]interface{} {
	switch tv := v.(type) {
	case []map[string]interface{}:
		return tv
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(tv))
		for _, x := range tv {
			if m, ok := x.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

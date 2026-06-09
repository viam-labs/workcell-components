package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// ToteStackModel — a stack of N identical boxes/totes.
//
// Useful for demos and to dress empty cell space (a stack of pallets
// next to the work zone, a tote tower in the buffer area, etc.).
// Pose anchor is the BOTTOM of the bottom-most box.
var ToteStackModel = resource.NewModel("viam", "workcell-components", "tote-stack")

const (
	defaultToteSizeX = 400.0
	defaultToteSizeY = 300.0
	defaultToteSizeZ = 250.0
	defaultToteCount = 4
)

var defaultToteColor = Color{R: 90, G: 130, B: 200, A: 1} // industrial blue

type ToteStackConfig struct {
	Label string `json:"label,omitempty"`

	BoxDimsMM *Vec3D `json:"box_dims_mm,omitempty"`
	Count     int    `json:"count,omitempty"`

	// StackAxis: "z" (default, stack upward) | "x" | "y".
	StackAxis string `json:"stack_axis,omitempty"`

	Color *Color `json:"color,omitempty"`

	VisualOptions
}

func (c *ToteStackConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	switch c.StackAxis {
	case "", "x", "y", "z":
	default:
		return nil, nil, fmt.Errorf("tote-stack: stack_axis must be x|y|z, got %q", c.StackAxis)
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, ToteStackModel,
		resource.Registration[resource.Resource, *ToteStackConfig]{
			Constructor: newToteStack,
		},
	)
}

type toteStack struct {
	*decorationBase
	dims  Vec3D
	count int
	axis  string
}

func newToteStack(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*ToteStackConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, ToteStackModel,
		conf.Frame, cfg.Label, defaultToteColor, cfg.Color, cfg.VisualOptions)
	d := Vec3D{X: defaultToteSizeX, Y: defaultToteSizeY, Z: defaultToteSizeZ}
	if cfg.BoxDimsMM != nil {
		if cfg.BoxDimsMM.X > 0 {
			d.X = cfg.BoxDimsMM.X
		}
		if cfg.BoxDimsMM.Y > 0 {
			d.Y = cfg.BoxDimsMM.Y
		}
		if cfg.BoxDimsMM.Z > 0 {
			d.Z = cfg.BoxDimsMM.Z
		}
	}
	count := cfg.Count
	if count <= 0 {
		count = defaultToteCount
	}
	axis := cfg.StackAxis
	if axis == "" {
		axis = "z"
	}
	return &toteStack{
		decorationBase: base,
		dims:           d,
		count:          count,
		axis:           axis,
	}, nil
}

func (t *toteStack) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(t.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(t.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return t.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return t.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(t.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(toteStackSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if d := m["box_dims_mm"]; d != nil {
			if dm, ok := d.(map[string]interface{}); ok {
				if x := asFloat(dm["x"]); x > 0 {
					t.dims.X = x
				}
				if y := asFloat(dm["y"]); y > 0 {
					t.dims.Y = y
				}
				if z := asFloat(dm["z"]); z > 0 {
					t.dims.Z = z
				}
			}
		}
		if c := int(asFloat(m["count"])); c > 0 {
			t.count = c
		}
		if ax, ok := m["stack_axis"].(string); ok && (ax == "x" || ax == "y" || ax == "z") {
			t.axis = ax
		}
		if err := t.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(t.attributesMap(), nil)
	}
	return nil, fmt.Errorf("tote-stack: unknown command %v", cmd)
}

// Caller must hold t.mu.
func (t *toteStack) attributesMap() map[string]interface{} {
	out := t.commonAttributesMap()
	out["box_dims_mm"] = map[string]interface{}{"x": t.dims.X, "y": t.dims.Y, "z": t.dims.Z}
	out["count"] = t.count
	out["stack_axis"] = t.axis
	return out
}

// toteStackSchema — webapp-edit schema.
func toteStackSchema() []schemaEntry {
	return []schemaEntry{
		vec3Entry("box_dims_mm", "Box dimensions", schemaGroupGeometry, "mm"),
		intEntry("count", "Box count", schemaGroupGeometry, 1, 30),
		enumEntry("stack_axis", "Stack axis", schemaGroupGeometry,
			[]string{"x", "y", "z"}),
		colorEntry("color", "Box color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Caller must hold t.mu.
func (t *toteStack) buildVisuals() []visualWire {
	if t.opts.Visible != nil && !*t.opts.Visible {
		return groupUnderFrame(t.name.Name, t.pose, false, nil)
	}
	out := make([]visualWire, 0, t.count)
	var dx, dy, dz float64
	var stepSize float64
	switch t.axis {
	case "x":
		stepSize = t.dims.X
	case "y":
		stepSize = t.dims.Y
	default: // z
		stepSize = t.dims.Z
	}
	for i := 0; i < t.count; i++ {
		switch t.axis {
		case "x":
			dx = float64(i)*stepSize + t.dims.X/2
		case "y":
			dy = float64(i)*stepSize + t.dims.Y/2
		default:
			dz = float64(i)*stepSize + t.dims.Z/2
		}
		out = append(out, boxAt(
			fmt.Sprintf("%s/tote-%d", t.name.Name, i),
			compose(t.pose, dx, dy, dz, 0, 0, 1, 0),
			t.dims.X, t.dims.Y, t.dims.Z,
			t.color, t.opts,
		))
	}
	return groupUnderFrame(t.name.Name, t.pose, t.opts.ShowAxes, out)
}

package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// FloorDecalModel — safety striping or zone label painted on the floor.
//
// Visual composition:
//   - One thin Box laid on the floor (the decal itself), OR
//   - When StripePattern="hazard": alternating yellow/black sub-Boxes
//     painted in stripes along the decal's length direction.
//
// Pose anchor is the center of the decal at floor level.
var FloorDecalModel = resource.NewModel("viam", "workcell-components", "floor-decal")

const (
	defaultDecalLengthMM    = 1500.0
	defaultDecalWidthMM     = 200.0
	defaultDecalThicknessMM = 2.0 // 2 mm = floor paint
	defaultHazardStripeMM   = 150.0
)

var (
	defaultDecalColor       = Color{R: 230, G: 200, B: 30, A: 1}  // safety yellow
	defaultHazardYellow     = Color{R: 240, G: 215, B: 40, A: 1}
	defaultHazardBlack      = Color{R: 20, G: 20, B: 22, A: 1}
)

type FloorDecalConfig struct {
	Label string `json:"label,omitempty"`

	LengthMM    float64 `json:"length_mm,omitempty"`
	WidthMM     float64 `json:"width_mm,omitempty"`
	ThicknessMM float64 `json:"thickness_mm,omitempty"`

	// StripePattern: "" (solid) | "hazard" (alternating yellow/black
	// diagonal stripes along the length axis).
	StripePattern string `json:"stripe_pattern,omitempty"`

	Color *Color `json:"color,omitempty"`

	VisualOptions
}

func (c *FloorDecalConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	switch c.StripePattern {
	case "", "hazard":
	default:
		return nil, nil, fmt.Errorf("floor-decal: stripe_pattern must be \"\" or \"hazard\", got %q", c.StripePattern)
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, FloorDecalModel,
		resource.Registration[resource.Resource, *FloorDecalConfig]{
			Constructor: newFloorDecal,
		},
	)
}

type floorDecal struct {
	*decorationBase
	length        float64
	width         float64
	thickness     float64
	stripePattern string
}

func newFloorDecal(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*FloorDecalConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, FloorDecalModel,
		conf.Frame, cfg.Label, defaultDecalColor, cfg.Color, cfg.VisualOptions)
	return &floorDecal{
		decorationBase: base,
		length:         defaultLen(cfg.LengthMM, defaultDecalLengthMM),
		width:          defaultLen(cfg.WidthMM, defaultDecalWidthMM),
		thickness:      defaultLen(cfg.ThicknessMM, defaultDecalThicknessMM),
		stripePattern:  cfg.StripePattern,
	}, nil
}

func (d *floorDecal) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(d.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(d.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return d.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return d.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(d.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(floorDecalSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if l := asFloat(m["length_mm"]); l > 0 {
			d.length = l
		}
		if w := asFloat(m["width_mm"]); w > 0 {
			d.width = w
		}
		if t := asFloat(m["thickness_mm"]); t > 0 {
			d.thickness = t
		}
		if sp, ok := m["stripe_pattern"].(string); ok {
			d.stripePattern = sp
		}
		if err := d.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(d.attributesMap(), nil)
	}
	return nil, fmt.Errorf("floor-decal: unknown command %v", cmd)
}

// Caller must hold d.mu.
func (d *floorDecal) attributesMap() map[string]interface{} {
	out := d.commonAttributesMap()
	out["length_mm"] = d.length
	out["width_mm"] = d.width
	out["thickness_mm"] = d.thickness
	out["stripe_pattern"] = d.stripePattern
	return out
}

// floorDecalSchema — webapp-edit schema.
func floorDecalSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("length_mm", "Length", schemaGroupGeometry, "mm", 50, 10000, 1),
		numEntry("width_mm", "Width", schemaGroupGeometry, "mm", 20, 5000, 1),
		numEntry("thickness_mm", "Thickness (paint height)", schemaGroupGeometry, "mm", 1, 50, 0.5),
		enumEntry("stripe_pattern", "Stripe pattern", schemaGroupGeometry,
			[]string{"", "hazard"}),
		// The hazard pattern is fixed yellow/black — color only applies
		// to the solid (no-stripe) decal.
		colorEntry("color", "Color", schemaGroupVisual).whenNot("stripe_pattern", "hazard"),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Caller must hold d.mu.
func (d *floorDecal) buildVisuals() []visualWire {
	if d.opts.Visible != nil && !*d.opts.Visible {
		return groupUnderFrame(d.name.Name, d.pose, false, nil)
	}
	// Position decal so its TOP sits at the pose's Z (so dragging on
	// the floor places the decal flush with the floor).
	localZ := d.thickness / 2

	if d.stripePattern != "hazard" {
		return groupUnderFrame(d.name.Name, d.pose, d.opts.ShowAxes, []visualWire{
			boxAt(
				fmt.Sprintf("%s/body", d.name.Name),
				compose(d.pose, 0, 0, localZ, 0, 0, 1, 0),
				d.length, d.width, d.thickness,
				d.color, d.opts,
			),
		})
	}

	// Hazard stripes: alternating yellow/black sub-Boxes along the
	// length axis. Stripe count derived from length / defaultHazardStripeMM.
	stripeLen := defaultHazardStripeMM
	count := int(d.length / stripeLen)
	if count < 2 {
		count = 2
	}
	actualLen := d.length / float64(count)
	startX := -d.length/2 + actualLen/2
	out := make([]visualWire, 0, count)
	for i := 0; i < count; i++ {
		localX := startX + float64(i)*actualLen
		c := defaultHazardYellow
		if i%2 == 1 {
			c = defaultHazardBlack
		}
		out = append(out, boxAt(
			fmt.Sprintf("%s/stripe-%d", d.name.Name, i),
			compose(d.pose, localX, 0, localZ, 0, 0, 1, 0),
			actualLen, d.width, d.thickness,
			c, d.opts,
		))
	}
	return groupUnderFrame(d.name.Name, d.pose, d.opts.ShowAxes, out)
}

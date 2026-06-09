package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// WorkcellBoundsModel — wireframe footprint of the workcell perimeter.
//
// Renders as 12 thin Capsule edges (the wireframe of a box), useful
// to delineate the cell's working volume without occluding what's
// inside. Drag-place via frame block; pose anchor is the centroid of
// the bounding volume.
var WorkcellBoundsModel = resource.NewModel("viam", "workcell-components", "workcell-bounds")

const (
	defaultBoundsLengthMM    = 3000.0
	defaultBoundsWidthMM     = 3000.0
	defaultBoundsHeightMM    = 2500.0
	defaultBoundsEdgeRadius  = 6.0
)

var defaultBoundsColor = Color{R: 60, G: 220, B: 220, A: 0.9} // cyan — distinguishable from yellow safety

type WorkcellBoundsConfig struct {
	Label string `json:"label,omitempty"`

	LengthMM     float64 `json:"length_mm,omitempty"`
	WidthMM      float64 `json:"width_mm,omitempty"`
	HeightMM     float64 `json:"height_mm,omitempty"`
	EdgeRadiusMM float64 `json:"edge_radius_mm,omitempty"`

	Color *Color `json:"color,omitempty"`

	VisualOptions
}

func (c *WorkcellBoundsConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, WorkcellBoundsModel,
		resource.Registration[resource.Resource, *WorkcellBoundsConfig]{
			Constructor: newWorkcellBounds,
		},
	)
}

type workcellBounds struct {
	*decorationBase
	length     float64
	width      float64
	height     float64
	edgeRadius float64
}

func newWorkcellBounds(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*WorkcellBoundsConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, WorkcellBoundsModel,
		conf.Frame, cfg.Label, defaultBoundsColor, cfg.Color, cfg.VisualOptions)
	return &workcellBounds{
		decorationBase: base,
		length:         defaultLen(cfg.LengthMM, defaultBoundsLengthMM),
		width:          defaultLen(cfg.WidthMM, defaultBoundsWidthMM),
		height:         defaultLen(cfg.HeightMM, defaultBoundsHeightMM),
		edgeRadius:     defaultLen(cfg.EdgeRadiusMM, defaultBoundsEdgeRadius),
	}, nil
}

func (b *workcellBounds) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(b.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(b.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return b.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return b.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(b.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(workcellBoundsSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if l := asFloat(m["length_mm"]); l > 0 {
			b.length = l
		}
		if w := asFloat(m["width_mm"]); w > 0 {
			b.width = w
		}
		if h := asFloat(m["height_mm"]); h > 0 {
			b.height = h
		}
		if er := asFloat(m["edge_radius_mm"]); er > 0 {
			b.edgeRadius = er
		}
		if err := b.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(b.attributesMap(), nil)
	}
	return nil, fmt.Errorf("workcell-bounds: unknown command %v", cmd)
}

// Caller must hold b.mu.
func (b *workcellBounds) attributesMap() map[string]interface{} {
	out := b.commonAttributesMap()
	out["length_mm"] = b.length
	out["width_mm"] = b.width
	out["height_mm"] = b.height
	out["edge_radius_mm"] = b.edgeRadius
	return out
}

// workcellBoundsSchema — webapp-edit schema.
func workcellBoundsSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("length_mm", "Length (X)", schemaGroupGeometry, "mm", 100, 20000, 1),
		numEntry("width_mm", "Width (Y)", schemaGroupGeometry, "mm", 100, 20000, 1),
		numEntry("height_mm", "Height (Z)", schemaGroupGeometry, "mm", 100, 10000, 1),
		numEntry("edge_radius_mm", "Edge radius", schemaGroupVisual, "mm", 1, 50, 0.5),
		colorEntry("color", "Color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Caller must hold b.mu. Emits 12 Capsule edges of the bounding box —
// 4 along each axis — in the pattern the library's BoundingBox
// composite uses (kept hand-rolled here so the workcell-scene poll
// doesn't need to flatten Composite types).
func (b *workcellBounds) buildVisuals() []visualWire {
	if b.opts.Visible != nil && !*b.opts.Visible {
		return groupUnderFrame(b.name.Name, b.pose, false, nil)
	}
	hx := b.length / 2
	hy := b.width / 2
	hz := b.height / 2
	r := b.edgeRadius
	out := make([]visualWire, 0, 12)
	i := 0
	add := func(x, y, z, ox, oy, oz, length float64) {
		out = append(out, capsuleAt(
			fmt.Sprintf("%s/edge-%02d", b.name.Name, i),
			compose(b.pose, x, y, z, ox, oy, oz, 0),
			r, length,
			b.color, b.opts,
		))
		i++
	}
	// 4 X-edges (long axis along +X)
	for _, sy := range []float64{-1, 1} {
		for _, sz := range []float64{-1, 1} {
			add(0, sy*hy, sz*hz, 1, 0, 0, b.length)
		}
	}
	// 4 Y-edges
	for _, sx := range []float64{-1, 1} {
		for _, sz := range []float64{-1, 1} {
			add(sx*hx, 0, sz*hz, 0, 1, 0, b.width)
		}
	}
	// 4 Z-edges
	for _, sx := range []float64{-1, 1} {
		for _, sy := range []float64{-1, 1} {
			add(sx*hx, sy*hy, 0, 0, 0, 1, b.height)
		}
	}
	return groupUnderFrame(b.name.Name, b.pose, b.opts.ShowAxes, out)
}

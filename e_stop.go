package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// EStopModel — red mushroom button on a yellow post.
//
// Visual composition:
//   - Box base plate (small square)
//   - Yellow Capsule post
//   - Red Sphere "mushroom" dome on top
//
// Compact footprint (~50 mm square base). Drag-place via frame block;
// pose anchor is the base center on the floor.
var EStopModel = resource.NewModel("viam", "workcell-components", "e-stop")

const (
	defaultEStopHeightMM   = 1100.0
	defaultEStopPostRadius = 22.0
	defaultEStopDomeRadius = 45.0
	defaultEStopBaseSize   = 100.0
	defaultEStopBaseHeight = 12.0
)

var (
	defaultEStopPostColor = Color{R: 230, G: 200, B: 30, A: 1} // safety yellow
	defaultEStopDomeColor = Color{R: 220, G: 30, B: 30, A: 1}  // bright red
	defaultEStopBaseColor = Color{R: 80, G: 80, B: 88, A: 1}   // dark grey plate
)

type EStopConfig struct {
	Label    string  `json:"label,omitempty"`
	HeightMM float64 `json:"height_mm,omitempty"`

	// Color override for the post. Dome is always red, base is grey.
	Color *Color `json:"color,omitempty"`

	VisualOptions
}

func (c *EStopConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, EStopModel,
		resource.Registration[resource.Resource, *EStopConfig]{
			Constructor: newEStop,
		},
	)
}

type eStop struct {
	*decorationBase
	height float64
}

func newEStop(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*EStopConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, EStopModel,
		conf.Frame, cfg.Label, defaultEStopPostColor, cfg.Color, cfg.VisualOptions)
	return &eStop{
		decorationBase: base,
		height:         defaultLen(cfg.HeightMM, defaultEStopHeightMM),
	}, nil
}

func (e *eStop) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(e.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(e.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return e.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return e.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(e.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(eStopSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if h := asFloat(m["height_mm"]); h > 0 {
			e.height = h
		}
		if err := e.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(e.attributesMap(), nil)
	}
	return nil, fmt.Errorf("e-stop: unknown command %v", cmd)
}

// Caller must hold e.mu.
func (e *eStop) attributesMap() map[string]interface{} {
	out := e.commonAttributesMap()
	out["height_mm"] = e.height
	return out
}

// eStopSchema — webapp-edit schema.
func eStopSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("height_mm", "Total height", schemaGroupGeometry, "mm", 200, 2500, 1),
		colorEntry("color", "Post color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Caller must hold e.mu.
func (e *eStop) buildVisuals() []visualWire {
	if e.opts.Visible != nil && !*e.opts.Visible {
		return groupUnderFrame(e.name.Name, e.pose, false, nil)
	}
	// Base plate sits on the floor.
	baseZ := defaultEStopBaseHeight / 2
	postLen := e.height - defaultEStopBaseHeight - defaultEStopDomeRadius
	if postLen <= 0 {
		postLen = e.height * 0.7
	}
	postZ := defaultEStopBaseHeight + postLen/2
	domeZ := defaultEStopBaseHeight + postLen
	children := []visualWire{
		boxAt(
			fmt.Sprintf("%s/base", e.name.Name),
			compose(e.pose, 0, 0, baseZ, 0, 0, 1, 0),
			defaultEStopBaseSize, defaultEStopBaseSize, defaultEStopBaseHeight,
			defaultEStopBaseColor, e.opts,
		),
		capsuleAt(
			fmt.Sprintf("%s/post", e.name.Name),
			compose(e.pose, 0, 0, postZ, 0, 0, 1, 0),
			defaultEStopPostRadius, postLen,
			e.color, e.opts,
		),
		sphereAt(
			fmt.Sprintf("%s/dome", e.name.Name),
			compose(e.pose, 0, 0, domeZ, 0, 0, 1, 0),
			defaultEStopDomeRadius,
			defaultEStopDomeColor, e.opts,
		),
	}
	return groupUnderFrame(e.name.Name, e.pose, e.opts.ShowAxes, children)
}

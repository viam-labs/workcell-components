package workcellcomponents

import (
	"context"
	"fmt"
	"math"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

// SafetyFenceModel — wire-mesh perimeter fence panel.
//
// Visual composition (all dims pulled live from config):
//   - 2 horizontal Capsule rails (top + bottom of the panel)
//   - N vertical Capsule posts every PostSpacingMM along the length
//   - 1 translucent Box "screen" for the mesh-fill look
//
// Pose comes from the standard `frame:` block so operators drag-place
// in the 3D viewer. The panel's local +X is the length direction
// (along the fence run); +Z is up.
//
// Frame origin: centerline of the panel BASE on the floor. Set
// frame.translation.z = 0 to plant the fence on the floor; the panel
// extends upward by height_mm.
var SafetyFenceModel = resource.NewModel("viam", "workcell-components", "safety-fence")

const (
	defaultFenceLengthMM   = 2000.0
	defaultFenceHeightMM   = 1800.0
	defaultFencePostSpacMM = 1000.0
	defaultFencePostRadius = 18.0
	defaultFenceRailRadius = 15.0
	defaultFenceScreenAlpha = 0.18
)

// Hi-vis safety-yellow default for the fence frame.
var defaultFenceColor = Color{R: 220, G: 200, B: 30, A: 1}

type SafetyFenceConfig struct {
	Label string `json:"label,omitempty"`

	LengthMM      float64 `json:"length_mm,omitempty"`
	HeightMM      float64 `json:"height_mm,omitempty"`
	PostSpacingMM float64 `json:"post_spacing_mm,omitempty"`

	// Color of the fence's metal frame (rails + posts).
	Color *Color `json:"color,omitempty"`

	// ScreenOpacity controls the translucent mesh-fill box. 0 = no
	// screen panel, just the frame. Defaults to 0.18 (light haze).
	ScreenOpacity *float64 `json:"screen_opacity,omitempty"`

	VisualOptions
}

func (c *SafetyFenceConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, SafetyFenceModel,
		resource.Registration[resource.Resource, *SafetyFenceConfig]{
			Constructor: newSafetyFence,
		},
	)
}

type safetyFence struct {
	*decorationBase
	length        float64
	height        float64
	postSpacing   float64
	screenOpacity float64
}

func newSafetyFence(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*SafetyFenceConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, SafetyFenceModel,
		conf.Frame, cfg.Label, defaultFenceColor, cfg.Color, cfg.VisualOptions)
	length := defaultLen(cfg.LengthMM, defaultFenceLengthMM)
	height := defaultLen(cfg.HeightMM, defaultFenceHeightMM)
	spacing := defaultLen(cfg.PostSpacingMM, defaultFencePostSpacMM)
	screen := defaultFenceScreenAlpha
	if cfg.ScreenOpacity != nil {
		screen = clamp01(*cfg.ScreenOpacity)
	}
	return &safetyFence{
		decorationBase: base,
		length:         length,
		height:         height,
		postSpacing:    spacing,
		screenOpacity:  screen,
	}, nil
}

func (s *safetyFence) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(s.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(s.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return s.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return s.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(s.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(safetyFenceSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if l := asFloat(m["length_mm"]); l > 0 {
			s.length = l
		}
		if h := asFloat(m["height_mm"]); h > 0 {
			s.height = h
		}
		if sp := asFloat(m["post_spacing_mm"]); sp > 0 {
			s.postSpacing = sp
		}
		if sov, ok := m["screen_opacity"]; ok {
			s.screenOpacity = clamp01(asFloat(sov))
		}
		if err := s.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(s.attributesMap(), nil)
	}
	return nil, fmt.Errorf("safety-fence: unknown command %v", cmd)
}

// Caller must hold s.mu.
func (s *safetyFence) attributesMap() map[string]interface{} {
	out := s.commonAttributesMap()
	out["length_mm"] = s.length
	out["height_mm"] = s.height
	out["post_spacing_mm"] = s.postSpacing
	out["screen_opacity"] = s.screenOpacity
	return out
}

// buildVisuals — composes the fence panel from rails + posts + screen.
// Local frame: +X = along the run, +Z = up. The fence's frame anchor
// is at the centerline of the panel base (so dragging the frame
// positions the fence's footprint center).
// Caller must hold s.mu.
func (s *safetyFence) buildVisuals() []visualWire {
	if s.opts.Visible != nil && !*s.opts.Visible {
		return groupUnderFrame(s.name.Name, s.pose, false, nil)
	}
	out := []visualWire{}

	// Rails (top + bottom). Capsules laid along +X.
	railLen := s.length
	railZTop := s.height
	railZBot := 50.0 // 50 mm above ground for the bottom rail
	for i, z := range []float64{railZTop, railZBot} {
		out = append(out, capsuleAt(
			fmt.Sprintf("%s/rail-%d", s.name.Name, i),
			compose(s.pose, 0, 0, z, 1, 0, 0, 0),
			defaultFenceRailRadius, railLen,
			s.color, s.opts,
		))
	}

	// Vertical posts every PostSpacingMM along +X. Always include
	// posts at both ends (-length/2 and +length/2) so the panel reads
	// as a complete frame.
	postCount := int(math.Floor(s.length/s.postSpacing)) + 1
	if postCount < 2 {
		postCount = 2
	}
	step := s.length / float64(postCount-1)
	startX := -s.length / 2
	postHeight := s.height + railZBot/2
	postZ := postHeight / 2
	for i := 0; i < postCount; i++ {
		localX := startX + float64(i)*step
		out = append(out, capsuleAt(
			fmt.Sprintf("%s/post-%d", s.name.Name, i),
			compose(s.pose, localX, 0, postZ, 0, 0, 1, 0),
			defaultFencePostRadius, postHeight,
			s.color, s.opts,
		))
	}

	// Translucent screen panel — a thin Box filling the rail/post
	// rectangle. Skipped if screen_opacity is zero.
	if s.screenOpacity > 0 {
		screenH := s.height - railZBot
		screenZ := railZBot + screenH/2
		screenColor := s.color
		screenColor.A = s.screenOpacity
		out = append(out, boxAt(
			fmt.Sprintf("%s/screen", s.name.Name),
			compose(s.pose, 0, 0, screenZ, 0, 0, 1, 0),
			s.length, 6, screenH,
			screenColor, s.opts,
		))
	}
	return groupUnderFrame(s.name.Name, s.pose, s.opts.ShowAxes, out)
}

// safetyFenceSchema — webapp-edit schema for safety-fence.
func safetyFenceSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("length_mm", "Panel length", schemaGroupGeometry, "mm", 100, 10000, 1),
		numEntry("height_mm", "Panel height", schemaGroupGeometry, "mm", 200, 3000, 1),
		numEntry("post_spacing_mm", "Post spacing", schemaGroupGeometry, "mm", 100, 3000, 1),
		numEntry("screen_opacity", "Screen mesh opacity", schemaGroupVisual, "", 0, 1, 0.05),
		colorEntry("color", "Frame color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Geometries implements resource.Shaped so the framesystem picks up
// a coarse footprint for collision checks. We use a thin axis-aligned
// box matching the panel footprint (mesh would be more accurate but
// motion-planner doesn't need that fidelity for a fence).
func (s *safetyFence) Geometries(_ context.Context, _ map[string]any) ([]spatialmath.Geometry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	box, err := spatialmath.NewBox(spatialmath.NewZeroPose(),
		r3vec(s.length, 50, s.height),
		s.label)
	if err != nil {
		return nil, fmt.Errorf("safety-fence Geometries: %w", err)
	}
	return []spatialmath.Geometry{box}, nil
}

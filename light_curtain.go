package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

// LightCurtainModel — paired tower light curtain.
//
// Two vertical Capsule columns (transmitter on -X, receiver on +X)
// span the local Y axis. Between them, BeamCount horizontal lines of
// short Capsule "beams" connect tower to tower, evenly spaced in Z.
// On a healthy curtain, beams pulse softly (Pulse animation). When
// the operator sets `state: "broken"` via set_attributes, beams turn
// red.
//
// Pose comes from the standard frame block. Local +X is span
// direction (perpendicular to flow); +Z is up.
//
// Frame origin: FLOOR midway between the two towers. Set
// frame.translation.z = 0 to plant the towers on the floor; they
// extend upward by height_mm.
var LightCurtainModel = resource.NewModel("viam", "workcell-components", "light-curtain")

const (
	defaultCurtainSpanMM   = 1500.0
	defaultCurtainHeightMM = 1200.0
	defaultCurtainBeams    = 12
	defaultCurtainTowerR   = 35.0
	defaultCurtainBeamR    = 6.0
)

var (
	defaultCurtainTowerColor      = Color{R: 35, G: 35, B: 40, A: 1}    // matte black
	defaultCurtainBeamSafeColor   = Color{R: 110, G: 220, B: 130, A: 1} // health green
	defaultCurtainBeamBrokenColor = Color{R: 240, G: 60, B: 60, A: 1}   // alarm red
)

type LightCurtainConfig struct {
	Label string `json:"label,omitempty"`

	SpanMM    float64 `json:"span_mm,omitempty"`
	HeightMM  float64 `json:"height_mm,omitempty"`
	BeamCount int     `json:"beam_count,omitempty"`

	// State: "safe" (default) | "broken". When broken, beams render
	// red and lose their pulse animation.
	State string `json:"state,omitempty"`

	// Color override for the towers (poles). Beam colors are intrinsic
	// to the state.
	Color *Color `json:"color,omitempty"`

	VisualOptions
}

func (c *LightCurtainConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, LightCurtainModel,
		resource.Registration[resource.Resource, *LightCurtainConfig]{
			Constructor: newLightCurtain,
		},
	)
}

type lightCurtain struct {
	*decorationBase
	span      float64
	height    float64
	beamCount int
	state     string
}

func newLightCurtain(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*LightCurtainConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, LightCurtainModel,
		conf.Frame, cfg.Label, defaultCurtainTowerColor, cfg.Color, cfg.VisualOptions)
	beams := cfg.BeamCount
	if beams <= 0 {
		beams = defaultCurtainBeams
	}
	state := cfg.State
	if state == "" {
		state = "safe"
	}
	return &lightCurtain{
		decorationBase: base,
		span:           defaultLen(cfg.SpanMM, defaultCurtainSpanMM),
		height:         defaultLen(cfg.HeightMM, defaultCurtainHeightMM),
		beamCount:      beams,
		state:          state,
	}, nil
}

func (l *lightCurtain) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(l.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(l.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return l.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return l.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(l.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(lightCurtainSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if sp := asFloat(m["span_mm"]); sp > 0 {
			l.span = sp
		}
		if h := asFloat(m["height_mm"]); h > 0 {
			l.height = h
		}
		if bc := int(asFloat(m["beam_count"])); bc > 0 {
			l.beamCount = bc
		}
		if st, ok := m["state"].(string); ok {
			l.state = st
		}
		if err := l.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(l.attributesMap(), nil)
	}
	return nil, fmt.Errorf("light-curtain: unknown command %v", cmd)
}

// Caller must hold l.mu.
func (l *lightCurtain) attributesMap() map[string]interface{} {
	out := l.commonAttributesMap()
	out["span_mm"] = l.span
	out["height_mm"] = l.height
	out["beam_count"] = l.beamCount
	out["state"] = l.state
	return out
}

// Caller must hold l.mu.
func (l *lightCurtain) buildVisuals() []visualWire {
	if l.opts.Visible != nil && !*l.opts.Visible {
		return groupUnderFrame(l.name.Name, l.pose, false, nil)
	}
	out := []visualWire{}

	// Two towers along +X / -X.
	towerLocalX := l.span / 2
	towerZ := l.height / 2
	for i, sign := range []float64{-1, 1} {
		out = append(out, capsuleAt(
			fmt.Sprintf("%s/tower-%d", l.name.Name, i),
			compose(l.pose, sign*towerLocalX, 0, towerZ, 0, 0, 1, 0),
			defaultCurtainTowerR, l.height,
			l.color, l.opts,
		))
	}

	// Beams between towers, evenly spaced in Z. Each beam is a short
	// Capsule running along +X with length = span - 2*towerR (so it
	// connects the inner faces).
	beamColor := defaultCurtainBeamSafeColor
	var beamAnim map[string]interface{}
	if l.state == "broken" {
		beamColor = defaultCurtainBeamBrokenColor
	} else {
		beamAnim = map[string]interface{}{
			"mode":     "pulse",
			"period_s": 1.6,
			"axis":     "z", // pulse beam radius (sphere/capsule radius mode)
			"amplitude_mm": 2.0,
		}
	}
	beamLength := l.span - 2*defaultCurtainTowerR
	if beamLength <= 0 {
		beamLength = l.span * 0.9
	}
	step := 0.0
	if l.beamCount > 1 {
		step = (l.height - 100) / float64(l.beamCount-1)
	}
	startZ := 80.0
	for i := 0; i < l.beamCount; i++ {
		z := startZ + float64(i)*step
		entry := capsuleAt(
			fmt.Sprintf("%s/beam-%02d", l.name.Name, i),
			compose(l.pose, 0, 0, z, 1, 0, 0, 0),
			defaultCurtainBeamR, beamLength,
			beamColor, l.opts,
		)
		if beamAnim != nil {
			entry.Animation = beamAnim
		}
		out = append(out, entry)
	}
	return groupUnderFrame(l.name.Name, l.pose, l.opts.ShowAxes, out)
}

// lightCurtainSchema — webapp-edit schema.
func lightCurtainSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("span_mm", "Span (tower-to-tower)", schemaGroupGeometry, "mm", 100, 5000, 1),
		numEntry("height_mm", "Height", schemaGroupGeometry, "mm", 200, 3000, 1),
		intEntry("beam_count", "Beam count", schemaGroupGeometry, 2, 48),
		enumEntry("state", "State", schemaGroupBehavior, []string{"safe", "broken"}),
		colorEntry("color", "Tower color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Geometries — light curtain doesn't block motion (it's optical). We
// emit no collision geometry; motion planner sees an empty list and
// skips collision checks for this resource.
func (l *lightCurtain) Geometries(_ context.Context, _ map[string]any) ([]spatialmath.Geometry, error) {
	return nil, nil
}

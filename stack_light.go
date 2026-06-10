package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// StackLightModel — multi-segment status beacon tower.
//
// Visual composition:
//   - Box base + Capsule mounting post
//   - N colored Capsule segments (the stack) on top of the post,
//     each segment a flat-ish disk of the configured color
//
// Each segment's state controls animation:
//   - "solid": segment renders at full opacity, no animation
//   - "flash": segment renders with a Flicker animation (~0.6 s period)
//   - "off":   segment renders dim (opacity 0.15), no animation
//
// Frame origin: center of the BASE on the floor. Set
// frame.translation.z = 0 to plant the stack-light on the floor; the
// post + segments extend upward.
var StackLightModel = resource.NewModel("viam", "workcell-components", "stack-light")

const (
	defaultStackPostHeightMM    = 600.0
	defaultStackSegmentHeightMM = 60.0
	defaultStackPostRadius      = 18.0
	defaultStackSegmentRadius   = 55.0
	defaultStackBaseSize        = 90.0
)

var (
	defaultStackBaseColor  = Color{R: 30, G: 30, B: 35, A: 1}
	defaultStackPostColor  = Color{R: 60, G: 60, B: 68, A: 1}
	defaultStackOffOpacity = 0.15
)

// Standard stack-light colors. Keyed strings make the config readable.
var stackLightColors = map[string]Color{
	"red":    {R: 240, G: 50, B: 50, A: 1},
	"yellow": {R: 240, G: 220, B: 60, A: 1},
	"orange": {R: 240, G: 150, B: 30, A: 1},
	"green":  {R: 80, G: 220, B: 100, A: 1},
	"blue":   {R: 70, G: 130, B: 240, A: 1},
	"white":  {R: 240, G: 240, B: 240, A: 1},
}

type StackLightConfig struct {
	Label string `json:"label,omitempty"`

	PostHeightMM    float64 `json:"post_height_mm,omitempty"`
	SegmentHeightMM float64 `json:"segment_height_mm,omitempty"`

	// Colors lists the stack segments bottom-up. Each entry must be
	// a named color (red/yellow/orange/green/blue/white).
	// Default: ["red", "yellow", "green"].
	Colors []string `json:"colors,omitempty"`

	// States, same order as Colors. Each entry is "solid" / "flash"
	// / "off". Default: all solid.
	States []string `json:"states,omitempty"`

	VisualOptions
}

func (c *StackLightConfig) Validate(_ string) ([]string, []string, error) {
	for _, name := range c.Colors {
		if _, ok := stackLightColors[name]; !ok {
			return nil, nil, fmt.Errorf("stack-light: unknown color %q (valid: red/yellow/orange/green/blue/white)", name)
		}
	}
	for _, st := range c.States {
		switch st {
		case "solid", "flash", "off":
		default:
			return nil, nil, fmt.Errorf("stack-light: unknown state %q (valid: solid/flash/off)", st)
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, StackLightModel,
		resource.Registration[resource.Resource, *StackLightConfig]{
			Constructor: newStackLight,
		},
	)
}

type stackLight struct {
	*decorationBase
	postHeight    float64
	segmentHeight float64
	colors        []string
	states        []string
}

func newStackLight(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*StackLightConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, StackLightModel,
		conf.Frame, cfg.Label, defaultStackBaseColor, nil, cfg.VisualOptions)
	colors := cfg.Colors
	if len(colors) == 0 {
		colors = []string{"red", "yellow", "green"}
	}
	states := cfg.States
	for len(states) < len(colors) {
		states = append(states, "solid")
	}
	return &stackLight{
		decorationBase: base,
		postHeight:     defaultLen(cfg.PostHeightMM, defaultStackPostHeightMM),
		segmentHeight:  defaultLen(cfg.SegmentHeightMM, defaultStackSegmentHeightMM),
		colors:         colors,
		states:         states,
	}, nil
}

func (s *stackLight) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
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
		return schemaToResponse(stackLightSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if h := asFloat(m["post_height_mm"]); h > 0 {
			s.postHeight = h
		}
		if h := asFloat(m["segment_height_mm"]); h > 0 {
			s.segmentHeight = h
		}
		if cs := coerceStringSlice(m["colors"]); cs != nil {
			s.colors = cs
			// Re-pad states to match new colors length.
			for len(s.states) < len(s.colors) {
				s.states = append(s.states, "solid")
			}
			if len(s.states) > len(s.colors) {
				s.states = s.states[:len(s.colors)]
			}
		}
		if ss := coerceStringSlice(m["states"]); ss != nil {
			s.states = ss
			for len(s.states) < len(s.colors) {
				s.states = append(s.states, "solid")
			}
		}
		if err := s.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(s.attributesMap(), nil)
	}
	return nil, fmt.Errorf("stack-light: unknown command %v", cmd)
}

// Caller must hold s.mu.
func (s *stackLight) attributesMap() map[string]interface{} {
	out := s.commonAttributesMap()
	out["post_height_mm"] = s.postHeight
	out["segment_height_mm"] = s.segmentHeight
	out["colors"] = s.colors
	out["states"] = s.states
	return out
}

// stackLightSchema — webapp-edit schema. colors[] and states[] are
// position-aligned enum lists: the i-th state controls the i-th color
// segment's animation.
func stackLightSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("post_height_mm", "Post height", schemaGroupGeometry, "mm", 100, 2000, 1),
		numEntry("segment_height_mm", "Segment height", schemaGroupGeometry, "mm", 20, 200, 1),
		enumListEntry("colors", "Layer colors (bottom-up)", schemaGroupGeometry,
			[]string{"red", "yellow", "orange", "green", "blue", "white"},
			"Order matters — bottom segment first."),
		enumListEntry("states", "Layer states (bottom-up)", schemaGroupBehavior,
			[]string{"solid", "flash", "off"},
			"One entry per color; flash uses the library's Flicker animation."),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Caller must hold s.mu.
func (s *stackLight) buildVisuals() []visualWire {
	if s.opts.Visible != nil && !*s.opts.Visible {
		return groupUnderFrame(s.name.Name, s.pose, false, nil)
	}
	out := []visualWire{}
	baseHeight := defaultEStopBaseHeight
	baseZ := baseHeight / 2
	postZ := baseHeight + s.postHeight/2

	out = append(out, boxAt(
		fmt.Sprintf("%s/base", s.name.Name),
		compose(s.pose, 0, 0, baseZ, 0, 0, 1, 0),
		defaultStackBaseSize, defaultStackBaseSize, baseHeight,
		defaultStackBaseColor, s.opts,
	))
	out = append(out, capsuleAt(
		fmt.Sprintf("%s/post", s.name.Name),
		compose(s.pose, 0, 0, postZ, 0, 0, 1, 0),
		defaultStackPostRadius, s.postHeight,
		defaultStackPostColor, s.opts,
	))

	// Segments stack from the top of the post upward.
	segStartZ := baseHeight + s.postHeight + s.segmentHeight/2
	for i, name := range s.colors {
		color, ok := stackLightColors[name]
		if !ok {
			color = stackLightColors["white"]
		}
		state := "solid"
		if i < len(s.states) {
			state = s.states[i]
		}
		applied := color
		var anim map[string]interface{}
		switch state {
		case "off":
			applied.A = defaultStackOffOpacity
		case "flash":
			anim = map[string]interface{}{
				"mode":       "flicker",
				"period_s":   0.6,
				"duty_cycle": 0.55,
			}
		}
		entry := capsuleAt(
			fmt.Sprintf("%s/segment-%d-%s", s.name.Name, i, name),
			compose(s.pose, 0, 0, segStartZ+float64(i)*s.segmentHeight, 0, 0, 1, 0),
			defaultStackSegmentRadius, s.segmentHeight,
			applied, s.opts,
		)
		if anim != nil {
			entry.Animation = anim
		}
		out = append(out, entry)
	}
	return groupUnderFrame(s.name.Name, s.pose, s.opts.ShowAxes, out)
}

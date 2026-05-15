package workcellcomponents

import (
	"context"
	"fmt"
	"sync"

	"github.com/golang/geo/r3"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/referenceframe"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

// PalletModel is the resource model for the pallet component. The
// pallet owns its own dimensions and color: a freshly-added pallet
// renders sensibly without any user-typed `frame.geometry` block, and
// dimensions / color can be updated live through DoCommand without a
// reconfigure (consumers like pack-sequencer fetch on-demand and pick
// up the new values).
//
// The pallet's pose still comes from the standard `frame:` block on
// the resource config — drag-and-save in the 3D viewer works as
// before. When a `frame.geometry` is present, its dimensions override
// the in-Config defaults; that's the legacy path. When absent, the
// component's intrinsic dimensions take over.
var PalletModel = resource.NewModel("viam", "workcell-components", "pallet")

// Standard GMA wooden-pallet dimensions (48" × 40" × ~6"). Used as
// fallbacks when neither Config attributes nor frame.geometry specify
// dimensions.
const (
	DefaultPalletWidthMM     = 1219.2 // 48 inches — pallet X
	DefaultPalletLengthMM    = 1016.0 // 40 inches — pallet Y
	DefaultPalletThicknessMM = 152.4  // 6 inches  — wooden base height (Z)
)

// Default wood-tan color for the pallet (≈ #c69961). Matches what a
// typical GMA wooden pallet looks like under workcell lighting.
var defaultPalletColor = Color{R: 198, G: 153, B: 97, A: 1}

// PalletConfig captures the pallet's dimensions, color, and label.
// All fields are optional with sensible defaults — a pallet with no
// configuration renders as a standard GMA-sized wooden pallet at the
// world origin.
//
// Dimensions in the config win over `frame.geometry` ONLY when
// frame.geometry is not set; if both are set, `frame.geometry` is
// authoritative (preserves the legacy drag-and-save flow).
type PalletConfig struct {
	Label string `json:"label,omitempty"`

	// Dimensions (mm). Zero values fall through to frame.geometry,
	// then to the GMA defaults.
	WidthMM     float64 `json:"width_mm,omitempty"`
	LengthMM    float64 `json:"length_mm,omitempty"`
	ThicknessMM float64 `json:"thickness_mm,omitempty"`

	// Color for the 3D-viewer rendering. RGB 0..255, opacity 0..1
	// (defaults to 1). When omitted, defaults to wood-tan.
	Color *Color `json:"color,omitempty"`
}

func (c *PalletConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, PalletModel,
		resource.Registration[resource.Resource, *PalletConfig]{
			Constructor: newPallet,
		},
	)
}

type pallet struct {
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	name   resource.Name
	logger logging.Logger

	mu sync.Mutex
	// Resolved at construction from conf.Frame + PalletConfig.
	// AlwaysRebuild means a frame edit (drag-and-save, or JSON edit)
	// replays newPallet, so these stay current without polling. The
	// `set_*` DoCommand verbs mutate these in place — consumers
	// querying via DoCommand pick up the new values immediately.
	pose                     spatialmath.Pose
	width, length, thickness float64
	color                    Color
	cfg                      PalletConfig
}

func newPallet(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*PalletConfig](conf)
	if err != nil {
		return nil, err
	}

	pose, w, l, t := palletPoseAndDimsFromFrame(conf.Frame, cfg)
	color := palletColor(cfg)

	logger.Infow("pallet configured",
		"x", pose.Point().X, "y", pose.Point().Y, "z", pose.Point().Z,
		"width_mm", w, "length_mm", l, "thickness_mm", t,
		"color_r", color.R, "color_g", color.G, "color_b", color.B,
	)

	return &pallet{
		name:      conf.ResourceName(),
		logger:    logger,
		pose:      pose,
		width:     w,
		length:    l,
		thickness: t,
		color:     color,
		cfg:       *cfg,
	}, nil
}

// palletPoseAndDimsFromFrame extracts the pallet's world pose and
// box-geometry dimensions, with the precedence:
//
//  1. frame.geometry (if set) — preserves legacy drag-and-save
//  2. PalletConfig dims (if set)
//  3. GMA defaults (the standard pallet shape)
func palletPoseAndDimsFromFrame(f *referenceframe.LinkConfig, cfg *PalletConfig) (spatialmath.Pose, float64, float64, float64) {
	// Start with the GMA defaults.
	w, l, t := DefaultPalletWidthMM, DefaultPalletLengthMM, DefaultPalletThicknessMM
	// Config attributes override defaults.
	if cfg.WidthMM > 0 {
		w = cfg.WidthMM
	}
	if cfg.LengthMM > 0 {
		l = cfg.LengthMM
	}
	if cfg.ThicknessMM > 0 {
		t = cfg.ThicknessMM
	}

	point := r3.Vector{}
	var orient spatialmath.Orientation = &spatialmath.OrientationVectorDegrees{OZ: 1}

	if f != nil {
		point = f.Translation
		if f.Orientation != nil {
			if o, err := f.Orientation.ParseConfig(); err == nil {
				orient = o
			}
		}
		// frame.geometry overrides Config dims (legacy drag-and-save).
		if f.Geometry != nil && f.Geometry.X > 0 && f.Geometry.Y > 0 && f.Geometry.Z > 0 {
			w = f.Geometry.X
			l = f.Geometry.Y
			t = f.Geometry.Z
		}
	}

	return spatialmath.NewPose(point, orient), w, l, t
}

func palletColor(cfg *PalletConfig) Color {
	if cfg.Color != nil {
		return *cfg.Color
	}
	return defaultPalletColor
}

func (p *pallet) Name() resource.Name { return p.name }

// DoCommand surface:
//
//	{"get_pose": true}        → {x, y, z, o_x, o_y, o_z, theta}
//	{"get_dimensions": true}  → {width_mm, length_mm, thickness_mm}
//	{"get_color": true}       → {r, g, b, opacity}
//	{"get_attributes": true}  → {label, width_mm, length_mm, thickness_mm,
//	                             color, pose}
//
//	{"set_dimensions": {"width_mm": …, "length_mm": …,
//	                    "thickness_mm": …}}
//	                          → updates dimensions in place, returns the
//	                             new {width_mm, length_mm, thickness_mm}
//	{"set_color": {"r": …, "g": …, "b": …, "opacity": …}}
//	                          → updates color in place, returns the new
//	                             {r, g, b, opacity}
//	{"set_attributes": {<any subset of width_mm/length_mm/thickness_mm/
//	                    color/label>}}
//	                          → batch update, returns get_attributes shape
//
// All set_* verbs are no-ops for omitted fields — partial updates are
// supported. Returns the post-update state so callers can confirm.
func (p *pallet) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(p.pose), nil
	}
	if _, ok := cmd["get_dimensions"]; ok {
		return p.dimsMap(), nil
	}
	if _, ok := cmd["get_color"]; ok {
		return p.color.toMap(), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return p.attributesMap(), nil
	}

	if v, ok := cmd["set_dimensions"]; ok {
		return p.setDimensions(v)
	}
	if v, ok := cmd["set_color"]; ok {
		return p.setColor(v)
	}
	if v, ok := cmd["set_attributes"]; ok {
		return p.setAttributes(v)
	}

	return nil, fmt.Errorf("pallet: unknown command %v", cmd)
}

// Caller must hold p.mu.
func (p *pallet) setDimensions(v interface{}) (map[string]interface{}, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("set_dimensions: expected object, got %T", v)
	}
	if w := asFloat(m["width_mm"]); w > 0 {
		p.width = w
	}
	if l := asFloat(m["length_mm"]); l > 0 {
		p.length = l
	}
	if t := asFloat(m["thickness_mm"]); t > 0 {
		p.thickness = t
	}
	p.logger.Infow("pallet dimensions updated via DoCommand",
		"width_mm", p.width, "length_mm", p.length, "thickness_mm", p.thickness)
	return p.dimsMap(), nil
}

// Caller must hold p.mu.
func (p *pallet) setColor(v interface{}) (map[string]interface{}, error) {
	c, ok := asColor(v)
	if !ok {
		return nil, fmt.Errorf("set_color: expected {r,g,b,opacity?} object, got %T", v)
	}
	if err := validateColor(c); err != nil {
		return nil, fmt.Errorf("set_color: %w", err)
	}
	p.color = c
	p.logger.Infow("pallet color updated via DoCommand",
		"r", c.R, "g", c.G, "b", c.B, "opacity", c.effectiveOpacity())
	return p.color.toMap(), nil
}

// Caller must hold p.mu.
func (p *pallet) setAttributes(v interface{}) (map[string]interface{}, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("set_attributes: expected object, got %T", v)
	}
	if _, ok := m["width_mm"]; ok {
		if w := asFloat(m["width_mm"]); w > 0 {
			p.width = w
		}
	}
	if _, ok := m["length_mm"]; ok {
		if l := asFloat(m["length_mm"]); l > 0 {
			p.length = l
		}
	}
	if _, ok := m["thickness_mm"]; ok {
		if t := asFloat(m["thickness_mm"]); t > 0 {
			p.thickness = t
		}
	}
	if cv, ok := m["color"]; ok {
		c, parsed := asColor(cv)
		if !parsed {
			return nil, fmt.Errorf("set_attributes: color must be a {r,g,b,opacity?} object")
		}
		if err := validateColor(c); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		p.color = c
	}
	if lbl, ok := m["label"].(string); ok {
		p.cfg.Label = lbl
	}
	p.logger.Infow("pallet attributes updated via DoCommand")
	return p.attributesMap(), nil
}

func (p *pallet) dimsMap() map[string]interface{} {
	return map[string]interface{}{
		"width_mm":     p.width,
		"length_mm":    p.length,
		"thickness_mm": p.thickness,
	}
}

// Caller must hold p.mu.
func (p *pallet) attributesMap() map[string]interface{} {
	return map[string]interface{}{
		"label":        p.cfg.Label,
		"width_mm":     p.width,
		"length_mm":    p.length,
		"thickness_mm": p.thickness,
		"color":        p.color.toMap(),
		"pose":         poseToWorldMap(p.pose),
	}
}

func poseToWorldMap(pose spatialmath.Pose) map[string]interface{} {
	pt := pose.Point()
	ov := pose.Orientation().OrientationVectorDegrees()
	return map[string]interface{}{
		"x": pt.X, "y": pt.Y, "z": pt.Z,
		"o_x": ov.OX, "o_y": ov.OY, "o_z": ov.OZ, "theta": ov.Theta,
	}
}

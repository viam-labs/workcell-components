package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// HMICabinetModel — operator HMI / control panel on a stand.
//
// Visual composition:
//   - Box body (cabinet enclosure)
//   - Box display panel on the front
//   - Capsule mounting arm reaching down to a small Box base
//
// Pose anchor is the bottom of the support post on the floor.
var HMICabinetModel = resource.NewModel("viam", "workcell-components", "hmi-cabinet")

const (
	defaultHMIBodyWMM      = 380.0
	defaultHMIBodyHMM      = 280.0
	defaultHMIBodyDepthMM  = 80.0
	defaultHMIScreenInset  = 30.0
	defaultHMIPostHeightMM = 1100.0
	defaultHMIPostRadius   = 20.0
	defaultHMIBaseEdge     = 200.0
	defaultHMIBaseHeight   = 14.0
)

var (
	defaultHMIBodyColor   = Color{R: 70, G: 75, B: 85, A: 1}
	defaultHMIScreenColor = Color{R: 30, G: 80, B: 130, A: 1}
)

type HMICabinetConfig struct {
	Label string `json:"label,omitempty"`

	BodyDimsMM   *Vec3D `json:"body_dims_mm,omitempty"`
	PostHeightMM float64 `json:"post_height_mm,omitempty"`

	BodyColor   *Color `json:"body_color,omitempty"`
	ScreenColor *Color `json:"screen_color,omitempty"`

	VisualOptions
}

func (c *HMICabinetConfig) Validate(_ string) ([]string, []string, error) {
	for _, c := range []*Color{c.BodyColor, c.ScreenColor} {
		if c == nil {
			continue
		}
		if err := validateColor(*c); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, HMICabinetModel,
		resource.Registration[resource.Resource, *HMICabinetConfig]{
			Constructor: newHMICabinet,
		},
	)
}

type hmiCabinet struct {
	*decorationBase
	bodyDims    Vec3D
	postHeight  float64
	bodyColor   Color
	screenColor Color
}

func newHMICabinet(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*HMICabinetConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, HMICabinetModel,
		conf.Frame, cfg.Label, defaultHMIBodyColor, nil, cfg.VisualOptions)
	bd := Vec3D{X: defaultHMIBodyWMM, Y: defaultHMIBodyDepthMM, Z: defaultHMIBodyHMM}
	if cfg.BodyDimsMM != nil {
		if cfg.BodyDimsMM.X > 0 {
			bd.X = cfg.BodyDimsMM.X
		}
		if cfg.BodyDimsMM.Y > 0 {
			bd.Y = cfg.BodyDimsMM.Y
		}
		if cfg.BodyDimsMM.Z > 0 {
			bd.Z = cfg.BodyDimsMM.Z
		}
	}
	bodyColor := defaultHMIBodyColor
	if cfg.BodyColor != nil {
		bodyColor = *cfg.BodyColor
	}
	screenColor := defaultHMIScreenColor
	if cfg.ScreenColor != nil {
		screenColor = *cfg.ScreenColor
	}
	return &hmiCabinet{
		decorationBase: base,
		bodyDims:       bd,
		postHeight:     defaultLen(cfg.PostHeightMM, defaultHMIPostHeightMM),
		bodyColor:      bodyColor,
		screenColor:    screenColor,
	}, nil
}

func (h *hmiCabinet) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(h.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(h.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return h.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return h.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(h.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(hmiCabinetSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if d := m["body_dims_mm"]; d != nil {
			if dm, ok := d.(map[string]interface{}); ok {
				if x := asFloat(dm["x"]); x > 0 {
					h.bodyDims.X = x
				}
				if y := asFloat(dm["y"]); y > 0 {
					h.bodyDims.Y = y
				}
				if z := asFloat(dm["z"]); z > 0 {
					h.bodyDims.Z = z
				}
			}
		}
		if p := asFloat(m["post_height_mm"]); p > 0 {
			h.postHeight = p
		}
		if cv, ok := m["body_color"]; ok {
			if c, parsed := asColor(cv); parsed {
				if err := validateColor(c); err != nil {
					return nil, fmt.Errorf("set_attributes body_color: %w", err)
				}
				h.bodyColor = c
			}
		}
		if cv, ok := m["screen_color"]; ok {
			if c, parsed := asColor(cv); parsed {
				if err := validateColor(c); err != nil {
					return nil, fmt.Errorf("set_attributes screen_color: %w", err)
				}
				h.screenColor = c
			}
		}
		if err := h.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(h.attributesMap(), nil)
	}
	return nil, fmt.Errorf("hmi-cabinet: unknown command %v", cmd)
}

// Caller must hold h.mu.
func (h *hmiCabinet) attributesMap() map[string]interface{} {
	out := h.commonAttributesMap()
	out["body_dims_mm"] = map[string]interface{}{"x": h.bodyDims.X, "y": h.bodyDims.Y, "z": h.bodyDims.Z}
	out["post_height_mm"] = h.postHeight
	out["body_color"] = h.bodyColor.toMap()
	out["screen_color"] = h.screenColor.toMap()
	return out
}

// hmiCabinetSchema — webapp-edit schema. Two color fields keep the
// body and screen independently styleable.
func hmiCabinetSchema() []schemaEntry {
	return []schemaEntry{
		vec3Entry("body_dims_mm", "Cabinet body dimensions", schemaGroupGeometry, "mm"),
		numEntry("post_height_mm", "Support post height", schemaGroupGeometry, "mm", 100, 2500, 1),
		colorEntry("body_color", "Body color", schemaGroupVisual),
		colorEntry("screen_color", "Screen color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Caller must hold h.mu.
func (h *hmiCabinet) buildVisuals() []visualWire {
	if h.opts.Visible != nil && !*h.opts.Visible {
		return groupUnderFrame(h.name.Name, h.pose, false, nil)
	}
	baseZ := defaultHMIBaseHeight / 2
	postZ := defaultHMIBaseHeight + h.postHeight/2
	bodyZ := defaultHMIBaseHeight + h.postHeight + h.bodyDims.Z/2
	screenZ := bodyZ
	// Screen sits on the +Y face of the body (a tablet-tilted-toward-
	// operator look) — push it +Y by half-body-depth + tiny offset.
	screenY := h.bodyDims.Y/2 + 2

	children := []visualWire{
		boxAt(
			fmt.Sprintf("%s/base", h.name.Name),
			compose(h.pose, 0, 0, baseZ, 0, 0, 1, 0),
			defaultHMIBaseEdge, defaultHMIBaseEdge, defaultHMIBaseHeight,
			h.bodyColor, h.opts,
		),
		capsuleAt(
			fmt.Sprintf("%s/post", h.name.Name),
			compose(h.pose, 0, 0, postZ, 0, 0, 1, 0),
			defaultHMIPostRadius, h.postHeight,
			h.bodyColor, h.opts,
		),
		boxAt(
			fmt.Sprintf("%s/body", h.name.Name),
			compose(h.pose, 0, 0, bodyZ, 0, 0, 1, 0),
			h.bodyDims.X, h.bodyDims.Y, h.bodyDims.Z,
			h.bodyColor, h.opts,
		),
		boxAt(
			fmt.Sprintf("%s/screen", h.name.Name),
			compose(h.pose, 0, screenY, screenZ, 0, 0, 1, 0),
			h.bodyDims.X-2*defaultHMIScreenInset, 4, h.bodyDims.Z-2*defaultHMIScreenInset,
			h.screenColor, h.opts,
		),
	}
	return groupUnderFrame(h.name.Name, h.pose, h.opts.ShowAxes, children)
}

package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// RobotPedestalModel — base under the robot arm.
//
// Visual composition:
//   - Box base plate (foot)
//   - Capsule cylindrical column body
//   - Box flange ring on top (the arm-mounting interface)
//
// Pose anchor is the bottom-of-pedestal on the floor.
var RobotPedestalModel = resource.NewModel("viam", "workcell-components", "robot-pedestal")

const (
	defaultPedestalHeightMM   = 500.0
	defaultPedestalDiameterMM = 250.0
	defaultPedestalBaseEdge   = 350.0
	defaultPedestalBaseHeight = 20.0
	defaultPedestalFlangeMM   = 20.0
)

var defaultPedestalColor = Color{R: 45, G: 45, B: 50, A: 1} // industrial dark grey

type RobotPedestalConfig struct {
	Label string `json:"label,omitempty"`

	HeightMM   float64 `json:"height_mm,omitempty"`
	DiameterMM float64 `json:"diameter_mm,omitempty"`

	Color *Color `json:"color,omitempty"`

	VisualOptions
}

func (c *RobotPedestalConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, RobotPedestalModel,
		resource.Registration[resource.Resource, *RobotPedestalConfig]{
			Constructor: newRobotPedestal,
		},
	)
}

type robotPedestal struct {
	*decorationBase
	height   float64
	diameter float64
}

func newRobotPedestal(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*RobotPedestalConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, RobotPedestalModel,
		conf.Frame, cfg.Label, defaultPedestalColor, cfg.Color, cfg.VisualOptions)
	return &robotPedestal{
		decorationBase: base,
		height:         defaultLen(cfg.HeightMM, defaultPedestalHeightMM),
		diameter:       defaultLen(cfg.DiameterMM, defaultPedestalDiameterMM),
	}, nil
}

func (p *robotPedestal) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(p.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(p.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return p.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return p.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(p.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(robotPedestalSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if h := asFloat(m["height_mm"]); h > 0 {
			p.height = h
		}
		if d := asFloat(m["diameter_mm"]); d > 0 {
			p.diameter = d
		}
		if err := p.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(p.attributesMap(), nil)
	}
	return nil, fmt.Errorf("robot-pedestal: unknown command %v", cmd)
}

// Caller must hold p.mu.
func (p *robotPedestal) attributesMap() map[string]interface{} {
	out := p.commonAttributesMap()
	out["height_mm"] = p.height
	out["diameter_mm"] = p.diameter
	return out
}

// robotPedestalSchema — webapp-edit schema.
func robotPedestalSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("height_mm", "Pedestal height", schemaGroupGeometry, "mm", 50, 2000, 1),
		numEntry("diameter_mm", "Column diameter", schemaGroupGeometry, "mm", 50, 1000, 1),
		colorEntry("color", "Color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Caller must hold p.mu.
func (p *robotPedestal) buildVisuals() []visualWire {
	if p.opts.Visible != nil && !*p.opts.Visible {
		return groupUnderFrame(p.name.Name, p.pose, false, nil)
	}
	baseZ := defaultPedestalBaseHeight / 2
	columnLen := p.height - defaultPedestalBaseHeight - defaultPedestalFlangeMM
	if columnLen <= 0 {
		columnLen = p.height * 0.85
	}
	columnZ := defaultPedestalBaseHeight + columnLen/2
	flangeZ := defaultPedestalBaseHeight + columnLen + defaultPedestalFlangeMM/2
	radius := p.diameter / 2
	children := []visualWire{
		boxAt(
			fmt.Sprintf("%s/base", p.name.Name),
			compose(p.pose, 0, 0, baseZ, 0, 0, 1, 0),
			defaultPedestalBaseEdge, defaultPedestalBaseEdge, defaultPedestalBaseHeight,
			p.color, p.opts,
		),
		capsuleAt(
			fmt.Sprintf("%s/column", p.name.Name),
			compose(p.pose, 0, 0, columnZ, 0, 0, 1, 0),
			radius, columnLen,
			p.color, p.opts,
		),
		boxAt(
			fmt.Sprintf("%s/flange", p.name.Name),
			compose(p.pose, 0, 0, flangeZ, 0, 0, 1, 0),
			p.diameter*1.1, p.diameter*1.1, defaultPedestalFlangeMM,
			p.color, p.opts,
		),
	}
	return groupUnderFrame(p.name.Name, p.pose, p.opts.ShowAxes, children)
}

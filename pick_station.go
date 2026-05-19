package workcellcomponents

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/golang/geo/r3"
	wcsh "github.com/viam-labs/viamkit/geom"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/referenceframe"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

// Vec3D is a plain 3D point/vector in mm. Re-exported from
// github.com/viam-labs/viamkit/geom so all workcell modules share
// the same JSON shape; the alias keeps the rest of this file readable.
type Vec3D = wcsh.Vec3D

// PickStationModel is the resource model for the pick station — the
// inbound conveyor (or static fixture) where boxes arrive for the
// palletizer to grab. Like the pallet, the pick-station owns its own
// dimensions and color: a freshly-added pick-station renders sensibly
// without any user-typed `frame.geometry`, and dimensions / color can
// be updated live through DoCommand so consumers (the palletizer)
// pick up the new values without needing a reconfigure.
//
// Pose still lives on the standard `frame:` block — drag-and-save in
// the 3D viewer works as before. Conveyor tilt — pitch incline
// (around the short axis) and roll incline (around the long axis) —
// also lives on `frame.orientation`.
var PickStationModel = resource.NewModel("viam", "workcell-components", "pick-station")

// Default surface dimensions for the pick-station. Sized for the
// curriculum's sim cell (small enough to fit comfortably in the
// simulated workspace, large enough to hold a typical box). For
// production cells, override via PickStationConfig dimensions or
// frame.geometry to match the real conveyor.
const (
	DefaultPickStationWidthMM     = 400.0
	DefaultPickStationLengthMM    = 400.0
	DefaultPickStationThicknessMM = 40.0
)

// Default metallic-grey for the pick-station (≈ #909094) — visually
// distinct from the wood-tan pallet so the two are easy to tell apart
// in the 3D viewer.
var defaultPickStationColor = Color{R: 144, G: 144, B: 148, A: 1}

// PickStationConfig holds the pickup-specific knobs that aren't part of
// the frame. All fields are optional with sensible defaults.
//
// Inclines: both inclines (pitch around the short axis, roll around
// the long axis) live in frame.orientation, decomposed at construction
// so the 3D viewer drag-and-save and the webapp incline inputs both
// write the same underlying pose. In Tait-Bryan ZYX terms used by the
// underlying spatialmath helpers: pitch_incline = the roll component
// (rotation around station-local X), roll_incline = the pitch
// component (rotation around station-local Y). The user-facing names
// reflect a conveyor whose long axis is "forward" (the flow direction).
//
// Coordinate frame: the station's local frame has its origin at the
// bottom-left corner of the conveyor surface (top face), matching the
// pallet convention: X along width, Y along length, Z=0 at the top of
// the surface. Box-related offsets are measured from that corner. To
// center a box on a 400×400 slab, use BoxOriginOffsetMM = {x:200, y:200}.
type PickStationConfig struct {
	// LowestPointHeightMM is the operator-measured distance from the
	// floor (world z = 0) to the lowest physical point of the conveyor
	// surface. Informational at runtime (frame.translation.z is the
	// actual position used for motion planning).
	LowestPointHeightMM float64 `json:"lowest_point_height_mm,omitempty"`

	// BoxOriginOffsetMM is where the box sits on the conveyor surface,
	// expressed in the station's local (bottom-left-top corner) frame.
	// x = along width, y = along length, z = box bottom above the
	// surface (usually 0 — box rests on the surface).
	BoxOriginOffsetMM *Vec3D `json:"box_origin_offset_mm,omitempty"`

	// BoxThetaDeg rotates the box around the station-local Z axis.
	// Positive = CCW viewed from above.
	BoxThetaDeg float64 `json:"box_theta_deg,omitempty"`

	// PickHomeZOffsetMM is the gripper's pre-grab waypoint above the
	// top of the box. Z-only offset — XY same as the vacuum point.
	PickHomeZOffsetMM float64 `json:"pick_home_z_offset_mm,omitempty"`

	// Dimensions (mm). Zero values fall through to frame.geometry,
	// then to the small-conveyor defaults.
	WidthMM     float64 `json:"width_mm,omitempty"`
	LengthMM    float64 `json:"length_mm,omitempty"`
	ThicknessMM float64 `json:"thickness_mm,omitempty"`

	// Color for the 3D-viewer rendering. RGB 0..255, opacity 0..1
	// (defaults to 1). When omitted, defaults to metallic grey.
	Color *Color `json:"color,omitempty"`

	// ConveyorDirection is the unit vector along which the conveyor
	// flows. The palletizer's "retract along the conveyor to clear
	// trailing boxes" move translates in this direction; an arrow
	// visual (future) renders at the conveyor surface. Conceptually
	// belongs on the conveyor, not the palletizer that consumes it.
	// Defaults to {0, 1, 0} (boxes flow in world +Y) when unset.
	ConveyorDirection *Vec3D `json:"conveyor_direction,omitempty"`

	Label string `json:"label,omitempty"`

	// VisualOptions — see pallet.VisualOptions for semantics. No
	// runtime effect in this release; future viz layer will read.
	VisualOptions
}

// DefaultConveyorDirection is the fallback for ConveyorDirection
// when neither the Config nor an explicit set_attributes call has
// supplied one.
var DefaultConveyorDirection = Vec3D{X: 0, Y: 1, Z: 0}

func (c *PickStationConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, PickStationModel,
		resource.Registration[resource.Resource, *PickStationConfig]{
			Constructor: newPickStation,
		},
	)
}

type pickStation struct {
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	name   resource.Name
	logger logging.Logger

	mu sync.Mutex
	// Resolved at construction. AlwaysRebuild means a frame edit (drag
	// in 3D viewer or attribute write through cloud config) replays
	// newPickStation. The `set_*` DoCommand verbs mutate these in
	// place; consumers querying via DoCommand pick up the new values
	// without a reconfigure.
	pose                     spatialmath.Pose
	width, length, thickness float64
	color                    Color
	cfg                      PickStationConfig
}

func newPickStation(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*PickStationConfig](conf)
	if err != nil {
		return nil, err
	}

	centerPose, w, l, t := pickStationPoseAndDimsFromFrame(conf.Frame, cfg)
	color := pickStationColor(cfg)
	// Tait-Bryan ZYX decomposition. The user-facing pitch_incline is
	// rotation around station-local X (the short / width axis), which
	// the math calls "roll"; user-facing roll_incline is rotation
	// around station-local Y (the long / length axis), which the math
	// calls "pitch". See PickStationConfig doc-comment for the
	// rationale.
	mathRoll, mathPitch, yaw := decomposeRPYDeg(centerPose.Orientation())
	pitchInclineDeg := mathRoll
	rollInclineDeg := mathPitch

	// The frame block describes the wood box's centroid (Viam
	// convention). Internal `pose` is the bottom-left-top corner so
	// box offsets are measured naturally from the corner — same
	// convention as the pallet.
	cornerOffset := spatialmath.NewPoseFromPoint(r3.Vector{X: -w / 2, Y: -l / 2, Z: t / 2})
	cornerPose := spatialmath.Compose(centerPose, cornerOffset)

	logger.Infow("pick-station configured",
		"corner_x", cornerPose.Point().X, "corner_y", cornerPose.Point().Y, "corner_z", cornerPose.Point().Z,
		"width_mm", w, "length_mm", l, "thickness_mm", t,
		"pitch_incline_deg", pitchInclineDeg, "roll_incline_deg", rollInclineDeg, "yaw_deg", yaw,
		"color_r", color.R, "color_g", color.G, "color_b", color.B,
	)

	return &pickStation{
		name:      conf.ResourceName(),
		logger:    logger,
		pose:      cornerPose,
		width:     w,
		length:    l,
		thickness: t,
		color:     color,
		cfg:       *cfg,
	}, nil
}

func pickStationPoseAndDimsFromFrame(f *referenceframe.LinkConfig, cfg *PickStationConfig) (spatialmath.Pose, float64, float64, float64) {
	// Start with small-conveyor defaults.
	w, l, t := DefaultPickStationWidthMM, DefaultPickStationLengthMM, DefaultPickStationThicknessMM
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

func pickStationColor(cfg *PickStationConfig) Color {
	if cfg.Color != nil {
		return *cfg.Color
	}
	return defaultPickStationColor
}

func (p *pickStation) Name() resource.Name { return p.name }

// DoCommand surface:
//
//	{"get_pose": true}             → station's frame pose (world)
//	{"get_dimensions": true}       → {width_mm, length_mm, thickness_mm}
//	{"get_color": true}            → {r, g, b, opacity}
//	{"get_pickup_pose": true}      → world pose where vacuum grabs
//	{"get_pick_home_pose": {"box_height_mm": …}}
//	                                → world pose of pre-grab waypoint
//	{"get_vacuum_pose": {"box_height_mm": …}}
//	                                → world pose at box top center
//	{"get_conveyor_direction": true}
//	                                → {x, y, z} unit vector
//	{"get_attributes": true}       → batch read of everything
//	{"get_status": true}           → {ok, name, model, dims_valid,
//	                                  color_valid, visible, show_axes}
//	{"get_summary": true}          → {"summary":"…"} one-line human
//
//	{"set_dimensions": {…}}        → updates dimensions in place
//	{"set_color": {…}}             → updates color in place
//	{"set_attributes": {…}}        → batch update of any subset of
//	                                  dims, color, label,
//	                                  box_origin_offset_mm,
//	                                  box_theta_deg,
//	                                  pick_home_z_offset_mm,
//	                                  conveyor_direction,
//	                                  show_axes, visible, opacity
//
// All set_* responses include `{"persisted": false, "hint":
// "…live until reconfigure…"}` so callers see that in-memory edits
// revert on next config reload.
func (p *pickStation) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(p.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		// p.pose is the bottom-left-top corner (set in newPickStation
		// to make box-origin offsets read naturally from the corner
		// outward). The visual pose is the centroid — compose the
		// inverse offset (+w/2, +l/2, -t/2) in local frame.
		centerOffset := spatialmath.NewPoseFromPoint(r3.Vector{X: p.width / 2, Y: p.length / 2, Z: -p.thickness / 2})
		return poseToWorldMap(spatialmath.Compose(p.pose, centerOffset)), nil
	}
	if _, ok := cmd["get_dimensions"]; ok {
		return p.dimsMap(), nil
	}
	if _, ok := cmd["get_color"]; ok {
		return p.color.toMap(), nil
	}
	if _, ok := cmd["get_pickup_pose"]; ok {
		return poseToWorldMap(p.pickupPose()), nil
	}
	if v, ok := cmd["get_pick_home_pose"]; ok {
		boxH := boxHeightArg(cmd, v)
		return poseToWorldMap(p.pickHomePose(boxH)), nil
	}
	if v, ok := cmd["get_vacuum_pose"]; ok {
		boxH := boxHeightArg(cmd, v)
		return poseToWorldMap(p.vacuumPose(boxH)), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return p.attributesMap(), nil
	}
	if _, ok := cmd["get_conveyor_direction"]; ok {
		v := p.effectiveConveyorDirection()
		return map[string]interface{}{"x": v.X, "y": v.Y, "z": v.Z}, nil
	}
	if _, ok := cmd["get_status"]; ok {
		return p.statusMap(), nil
	}
	if _, ok := cmd["get_summary"]; ok {
		return map[string]interface{}{"summary": p.summaryString()}, nil
	}

	if v, ok := cmd["set_dimensions"]; ok {
		return withPersistHint(p.setDimensions(v))
	}
	if v, ok := cmd["set_color"]; ok {
		return withPersistHint(p.setColor(v))
	}
	if v, ok := cmd["set_attributes"]; ok {
		return withPersistHint(p.setAttributes(v))
	}

	return nil, fmt.Errorf("pick-station: unknown command %v", cmd)
}

// effectiveConveyorDirection returns the configured ConveyorDirection
// or the default if unset. Caller must hold p.mu.
func (p *pickStation) effectiveConveyorDirection() Vec3D {
	if p.cfg.ConveyorDirection != nil {
		return *p.cfg.ConveyorDirection
	}
	return DefaultConveyorDirection
}

// statusMap returns the runtime health snapshot. Caller must hold p.mu.
func (p *pickStation) statusMap() map[string]interface{} {
	visible := true
	if p.cfg.Visible != nil {
		visible = *p.cfg.Visible
	}
	return map[string]interface{}{
		"ok":          true,
		"model":       PickStationModel.String(),
		"name":        p.name.String(),
		"dims_valid":  p.width > 0 && p.length > 0 && p.thickness > 0,
		"color_valid": validateColor(p.color) == nil,
		"visible":     visible,
		"show_axes":   p.cfg.ShowAxes,
	}
}

// summaryString builds a one-line human description. Caller must hold p.mu.
func (p *pickStation) summaryString() string {
	label := p.cfg.Label
	if label == "" {
		label = p.name.Name
	}
	pt := p.pose.Point()
	conv := p.effectiveConveyorDirection()
	return fmt.Sprintf("pick-station %q, %.1fx%.1fx%.1f mm, color (%d,%d,%d), corner at (%.1f, %.1f, %.1f), conveyor (%.2f, %.2f, %.2f)",
		label, p.width, p.length, p.thickness,
		p.color.R, p.color.G, p.color.B,
		pt.X, pt.Y, pt.Z,
		conv.X, conv.Y, conv.Z)
}

// Caller must hold p.mu.
func (p *pickStation) setDimensions(v interface{}) (map[string]interface{}, error) {
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
	p.logger.Infow("pick-station dimensions updated via DoCommand",
		"width_mm", p.width, "length_mm", p.length, "thickness_mm", p.thickness)
	return p.dimsMap(), nil
}

// Caller must hold p.mu.
func (p *pickStation) setColor(v interface{}) (map[string]interface{}, error) {
	c, ok := asColor(v)
	if !ok {
		return nil, fmt.Errorf("set_color: expected {r,g,b,opacity?} object, got %T", v)
	}
	if err := validateColor(c); err != nil {
		return nil, fmt.Errorf("set_color: %w", err)
	}
	p.color = c
	p.logger.Infow("pick-station color updated via DoCommand",
		"r", c.R, "g", c.G, "b", c.B, "opacity", c.effectiveOpacity())
	return p.color.toMap(), nil
}

// Caller must hold p.mu.
func (p *pickStation) setAttributes(v interface{}) (map[string]interface{}, error) {
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
	if v2, ok := m["box_theta_deg"]; ok {
		p.cfg.BoxThetaDeg = asFloat(v2)
	}
	if v2, ok := m["pick_home_z_offset_mm"]; ok {
		p.cfg.PickHomeZOffsetMM = asFloat(v2)
	}
	if v2, ok := m["box_origin_offset_mm"].(map[string]interface{}); ok {
		p.cfg.BoxOriginOffsetMM = &Vec3D{
			X: asFloat(v2["x"]),
			Y: asFloat(v2["y"]),
			Z: asFloat(v2["z"]),
		}
	}
	if v2, ok := m["conveyor_direction"].(map[string]interface{}); ok {
		p.cfg.ConveyorDirection = &Vec3D{
			X: asFloat(v2["x"]),
			Y: asFloat(v2["y"]),
			Z: asFloat(v2["z"]),
		}
	}
	applyVisualOptions(&p.cfg.VisualOptions, m)
	p.logger.Infow("pick-station attributes updated via DoCommand")
	return p.attributesMap(), nil
}

func (p *pickStation) dimsMap() map[string]interface{} {
	return map[string]interface{}{
		"width_mm":     p.width,
		"length_mm":    p.length,
		"thickness_mm": p.thickness,
	}
}

// Caller must hold p.mu.
func (p *pickStation) attributesMap() map[string]interface{} {
	// pitch_incline = math roll (around X / short axis);
	// roll_incline  = math pitch (around Y / long axis).
	mathRoll, mathPitch, yaw := decomposeRPYDeg(p.pose.Orientation())
	conv := p.effectiveConveyorDirection()
	out := map[string]interface{}{
		"label":                  p.cfg.Label,
		"width_mm":               p.width,
		"length_mm":              p.length,
		"thickness_mm":           p.thickness,
		"color":                  p.color.toMap(),
		"lowest_point_height_mm": p.cfg.LowestPointHeightMM,
		"pitch_incline_deg":      mathRoll,
		"roll_incline_deg":       mathPitch,
		"yaw_deg":                yaw,
		"box_origin_offset_mm":   p.boxOffsetMap(),
		"box_theta_deg":          p.cfg.BoxThetaDeg,
		"pick_home_z_offset_mm":  p.cfg.PickHomeZOffsetMM,
		"conveyor_direction":     map[string]interface{}{"x": conv.X, "y": conv.Y, "z": conv.Z},
		"pose":                   poseToWorldMap(p.pose),
		"pickup_pose":            poseToWorldMap(p.pickupPose()),
		"summary":                p.summaryString(),
	}
	mergeVisualOptions(out, p.cfg.VisualOptions)
	return out
}

func (p *pickStation) boxOffsetMap() map[string]interface{} {
	v := p.cfg.BoxOriginOffsetMM
	if v == nil {
		v = &Vec3D{}
	}
	return map[string]interface{}{"x": v.X, "y": v.Y, "z": v.Z}
}

// pickupPose returns the world-frame pose where the vacuum grabs the
// box. The vacuum lands at the center of the box's TOP face, but
// without knowing the box height we just return the box origin pose
// (XY position on the surface, Z above-surface from the offset's z,
// plus the box theta yaw). Consumers that need the actual gripper
// pose should add box_height to Z themselves, or call
// get_pick_home_pose with box_height_mm.
//
// p.pose already encodes both inclines (pitch + roll, via
// frame.orientation); composing with the box offset and theta tilts
// the gripper to follow the surface naturally, oriented along the
// box's local axes.
// Caller must hold p.mu.
func (p *pickStation) pickupPose() spatialmath.Pose {
	v := p.cfg.BoxOriginOffsetMM
	if v == nil {
		v = &Vec3D{}
	}
	offsetPose := spatialmath.NewPose(
		r3.Vector{X: v.X, Y: v.Y, Z: v.Z},
		&spatialmath.OrientationVectorDegrees{
			OZ: -1, Theta: p.cfg.BoxThetaDeg,
		},
	)
	return spatialmath.Compose(p.pose, offsetPose)
}

// vacuumPose returns the world-frame pose where the vacuum actually
// lands: top center of the box (box origin XY + boxHeightMM on Z),
// gripper straight down in station-local frame with the box theta
// yaw applied. Caller supplies boxHeightMM from the pack config.
// Caller must hold p.mu.
func (p *pickStation) vacuumPose(boxHeightMM float64) spatialmath.Pose {
	v := p.cfg.BoxOriginOffsetMM
	if v == nil {
		v = &Vec3D{}
	}
	offsetPose := spatialmath.NewPose(
		r3.Vector{X: v.X, Y: v.Y, Z: v.Z + boxHeightMM},
		&spatialmath.OrientationVectorDegrees{
			OZ: -1, Theta: p.cfg.BoxThetaDeg,
		},
	)
	return spatialmath.Compose(p.pose, offsetPose)
}

// pickHomePose returns the world-frame pose of the pre-grab waypoint:
// directly above the top of the box by PickHomeZOffsetMM. The arm
// parks here, descends to grab, lifts back to here. boxHeightMM is
// supplied by the caller (typically pulled from pack-sequencer) since
// the pick-station doesn't own box dimensions. Caller must hold p.mu.
func (p *pickStation) pickHomePose(boxHeightMM float64) spatialmath.Pose {
	v := p.cfg.BoxOriginOffsetMM
	if v == nil {
		v = &Vec3D{}
	}
	z := v.Z + boxHeightMM + p.cfg.PickHomeZOffsetMM
	offsetPose := spatialmath.NewPose(
		r3.Vector{X: v.X, Y: v.Y, Z: z},
		&spatialmath.OrientationVectorDegrees{
			OZ: -1, Theta: p.cfg.BoxThetaDeg,
		},
	)
	return spatialmath.Compose(p.pose, offsetPose)
}

// Geometries implements resource.Shaped so the framesystem picks up
// the pick-station's footprint for motion-planner collision checks
// without the operator typing a `frame.geometry` block. The geometry
// is a single Box centered at the resource's frame origin (Viam
// convention: frame.translation places a geometry's centroid).
//
// Reads the live width/length/thickness — `set_dimensions` /
// `set_attributes` updates take effect on the next motion plan
// without a reconfigure of dependent modules.
func (p *pickStation) Geometries(_ context.Context, _ map[string]any) ([]spatialmath.Geometry, error) {
	p.mu.Lock()
	w, l, t := p.width, p.length, p.thickness
	label := p.cfg.Label
	p.mu.Unlock()
	if label == "" {
		label = "pick-station"
	}
	box, err := spatialmath.NewBox(spatialmath.NewZeroPose(), r3.Vector{X: w, Y: l, Z: t}, label)
	if err != nil {
		return nil, fmt.Errorf("pick-station Geometries: %w", err)
	}
	return []spatialmath.Geometry{box}, nil
}

// boxHeightArg pulls box_height_mm from either the top-level cmd map
// or the verb's own value when it's an object. Lets callers use
// either calling convention:
//
//	{"get_vacuum_pose": true, "box_height_mm": 60}     // flat
//	{"get_vacuum_pose": {"box_height_mm": 60}}         // nested
//
// The nested form matches the convention every other arg-taking verb
// in this module already uses (set_dimensions, set_color,
// set_attributes), so the dryrun-natural choice — and the one the
// handler used to silently ignore — now works.
func boxHeightArg(cmd map[string]interface{}, verbValue interface{}) float64 {
	if m, ok := verbValue.(map[string]interface{}); ok {
		if h := asFloat(m["box_height_mm"]); h > 0 {
			return h
		}
	}
	return asFloat(cmd["box_height_mm"])
}

// decomposeRPYDeg returns roll/pitch/yaw (degrees) from any
// spatialmath.Orientation, using the Tait-Bryan z-y'-x'' convention
// that matches spatialmath.EulerAngles.
func decomposeRPYDeg(o spatialmath.Orientation) (roll, pitch, yaw float64) {
	if o == nil {
		return 0, 0, 0
	}
	ea := o.EulerAngles()
	if ea == nil {
		return 0, 0, 0
	}
	const rad2deg = 180.0 / math.Pi
	return ea.Roll * rad2deg, ea.Pitch * rad2deg, ea.Yaw * rad2deg
}

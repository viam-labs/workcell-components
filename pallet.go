package workcellcomponents

import (
	"context"
	"fmt"
	"sync"

	"github.com/golang/geo/r3"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/components/sensor"
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
// Frame origin: the bounding-box CENTROID (Viam convention — dragging
// the frame in the 3D viewer drags the visible box's center). To rest
// the pallet on the floor, set frame.translation.z = thickness_mm / 2
// (~76 mm for the GMA default). Rotations in frame.orientation rotate
// around this centroid.
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

	// Style controls how the pallet renders in the 3D viewer: how
	// many top-deck slats, whether stringers or blocks underneath.
	// One of "stringer" (default — 3 long stringers), "block" (9-block
	// grid), "plastic" (single-piece slate-grey). Visual only; motion-
	// planner collision geometry stays a single bounding box.
	Style string `json:"style,omitempty"`

	// TrayDock names a tray-dock sensor on the same machine. When set,
	// the pallet renders the tray exchange the sensor is simulating:
	// during a dispatch the tray slides out along station +Y carrying a
	// load silhouette, and the empty replacement slides in behind it.
	// The sensor becomes a required dependency of the pallet; without
	// the attribute the pallet renders exactly as before.
	TrayDock string `json:"tray_dock,omitempty"`

	// ExchangeTravelMM is how far the tray travels off the dock before
	// it disappears (and where the replacement appears). Default 1200.
	ExchangeTravelMM float64 `json:"exchange_travel_mm,omitempty"`

	// ExchangeLoadHeightMM sizes the load silhouette riding the
	// outbound tray, so a dispatched tray reads as full. Default 200;
	// set 0 to hide it.
	ExchangeLoadHeightMM *float64 `json:"exchange_load_height_mm,omitempty"`

	// VisualOptions are forward-looking knobs the future workcell
	// visualization layer reads. Declaring them now stabilizes the
	// Config schema so consumers (webapp form fields, contracts
	// types) don't churn when the viz layer lands. No behavioral
	// effect in this release — the values flow through get_attributes
	// and set_attributes so callers can persist their preference.
	VisualOptions
}

// VisualOptions are shared between pallet and pick-station. The
// future workcell-visualizer reads them; for now they're configuration
// state with no runtime effect.
type VisualOptions struct {
	// ShowAxes, when true, asks the viz layer to render a coordinate-
	// axes helper at the component's frame origin. Defaults false.
	ShowAxes bool `json:"show_axes,omitempty"`

	// Visible, when set to false, asks the viz layer to hide the
	// component in the 3D scene. Pointer so a missing value means
	// "use default" (true) rather than "explicitly hidden" (false).
	// Does NOT affect motion-planner visibility — collision geometry
	// still applies.
	Visible *bool `json:"visible,omitempty"`

	// Opacity is a component-wide opacity multiplier (0..1). Composes
	// with the color's own opacity at render time. Pointer so unset
	// means "no override." Defaults to 1.0 when nil.
	Opacity *float64 `json:"opacity,omitempty"`
}

// PalletHomeDefaultSafetyHeightMM is the default safety altitude for
// `get_pallet_home_pose` — far enough above the top face that a
// retracted gripper carrying a typical box (~100 mm tall) won't clip
// the pallet's top boxes. Callers can override per-call.
const PalletHomeDefaultSafetyHeightMM = 200.0

func (c *PalletConfig) Validate(_ string) ([]string, []string, error) {
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	if c.TrayDock != "" {
		// Required, deliberately: optional dependencies do not order
		// the build graph (see pick-station's infeed sensor).
		return []string{sensor.Named(c.TrayDock).String()}, nil, nil
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

	// dock is the paired tray-dock sensor, nil when the config names
	// none.
	dock sensor.Sensor
	// dockReadFailing tracks read health so failures log once per
	// outage rather than once per scene tick.
	dockReadFailing bool
	color           Color
	cfg             PalletConfig
}

func newPallet(
	_ context.Context,
	deps resource.Dependencies,
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

	var dock sensor.Sensor
	if cfg.TrayDock != "" {
		snsr, err := sensor.FromDependencies(deps, cfg.TrayDock)
		if err != nil {
			// Unreachable in practice: Validate declares the sensor as
			// a required dependency, so the graph withholds this
			// constructor until it exists. Fail loudly if it happens.
			return nil, fmt.Errorf("tray_dock %q: %w", cfg.TrayDock, err)
		}
		dock = snsr
	}

	return &pallet{
		name:      conf.ResourceName(),
		logger:    logger,
		pose:      pose,
		width:     w,
		length:    l,
		thickness: t,
		color:     color,
		cfg:       *cfg,
		dock:      dock,
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
//	{"get_pose": true}             → {x, y, z, o_x, o_y, o_z, theta}
//	{"get_dimensions": true}       → {width_mm, length_mm, thickness_mm}
//	{"get_color": true}            → {r, g, b, opacity}
//	{"get_attributes": true}       → batch read (everything below)
//
//	{"get_pallet_home_pose": true | {"safety_height_mm": h}}
//	                               → world-frame pose above pallet
//	                                  center, gripper-down. Default
//	                                  safety_height_mm = 200.
//	{"get_top_face_center": true}  → world-frame pose at the center of
//	                                  the pallet's top face, gripper-
//	                                  down. (= get_pallet_home_pose
//	                                  with safety_height_mm = 0.)
//	{"get_corner_poses": true}     → {"corners":[pose, pose, pose, pose]}
//	                                  world-frame poses of the four top-
//	                                  face corners, gripper-down. Order
//	                                  is CCW from bottom-left: (0,0),
//	                                  (w,0), (w,l), (0,l) in pallet-local.
//	{"get_status": true}           → {ok, name, dims_valid, color_valid,
//	                                  visible, model}
//	{"get_summary": true}          → {"summary":"…"} one-line human str
//
//	{"set_dimensions": {…}}        → updates dimensions in place
//	{"set_color": {…}}             → updates color in place
//	{"set_attributes": {…}}        → batch update of any subset of
//	                                  width/length/thickness/color/label/
//	                                  show_axes/visible/opacity
//
// All set_* verbs are no-ops for omitted fields — partial updates are
// supported. Their responses include `{"persisted": false, "hint":
// "…"}` so callers see that live mutation does NOT survive a
// reconfigure (the cell config wins on next reload).
func (p *pallet) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	// The dock sensor read round-trips through viam-server; do it
	// before taking the component lock so a slow sensor cannot stall
	// pose queries.
	var exchange *trayExchangeState
	if _, ok := cmd["get_visuals"]; ok {
		exchange = p.exchangeState(ctx)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(p.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		// Pallet's p.pose IS the centroid (Viam frame convention —
		// dragging the frame in the 3D viewer drags the visible box),
		// so visual pose == get_pose. Symmetric with pick-station's
		// get_visual_pose which composes the corner→center offset.
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
	if _, ok := cmd["get_visuals"]; ok {
		travel := p.cfg.ExchangeTravelMM
		if travel <= 0 {
			travel = defaultExchangeTravelMM
		}
		entries := palletVisuals(
			p.name.Name,
			p.pose, p.width, p.length, p.thickness,
			p.color, p.cfg.Style, p.cfg.VisualOptions,
			exchange,
			p.cfg.TrayDock != "",
			travel,
		)
		out, err := visualsToMaps(entries)
		if err != nil {
			return nil, fmt.Errorf("get_visuals: %w", err)
		}
		return map[string]interface{}{"visuals": out}, nil
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(palletSchema())
	}
	if v, ok := cmd["get_pallet_home_pose"]; ok {
		safety := safetyHeightArg(v, PalletHomeDefaultSafetyHeightMM)
		return poseToWorldMap(p.palletHomePose(safety)), nil
	}
	if _, ok := cmd["get_top_face_center"]; ok {
		return poseToWorldMap(p.palletHomePose(0)), nil
	}
	if _, ok := cmd["get_corner_poses"]; ok {
		corners := p.cornerPoses()
		out := make([]map[string]interface{}, 0, 4)
		for _, c := range corners {
			out = append(out, poseToWorldMap(c))
		}
		return map[string]interface{}{"corners": out}, nil
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

	return nil, fmt.Errorf("pallet: unknown command %v", cmd)
}

// palletHomePose returns a world-frame pose at the center of the
// pallet's top face plus `safetyHeightMM` in world-Z. Gripper-down
// orientation. Caller must hold p.mu.
//
// Note: the pallet's `p.pose` is the wood's centroid (Viam frame
// convention — dragging the frame in the 3D viewer drags the visible
// box). Top-face center is the centroid lifted by thickness/2.
func (p *pallet) palletHomePose(safetyHeightMM float64) spatialmath.Pose {
	// Compose with (0, 0, t/2 + safetyHeight) in pallet-local frame
	// to land above the top face's center.
	localOffset := spatialmath.NewPose(
		r3.Vector{X: 0, Y: 0, Z: p.thickness/2 + safetyHeightMM},
		&spatialmath.OrientationVectorDegrees{OZ: 1}, // identity OV in local frame
	)
	composed := spatialmath.Compose(p.pose, localOffset)
	// Override orientation: gripper straight down (OZ=-1) regardless
	// of pallet's local orientation. The pallet's own orientation
	// applies to the top-face normal (via the compose above); the
	// caller wants to grasp downward in world frame.
	return spatialmath.NewPose(composed.Point(),
		&spatialmath.OrientationVectorDegrees{OZ: -1, Theta: 0})
}

// cornerPoses returns the four world-frame poses at the top-face
// corners. Order is CCW viewed from above: BL (-w/2, -l/2), BR
// (+w/2, -l/2), TR (+w/2, +l/2), TL (-w/2, +l/2) — all in pallet-
// local frame, then composed with the pallet's centroid pose.
// Gripper-down orientation at each corner. Caller must hold p.mu.
func (p *pallet) cornerPoses() [4]spatialmath.Pose {
	halfW, halfL, halfT := p.width/2, p.length/2, p.thickness/2
	corners := [4]r3.Vector{
		{X: -halfW, Y: -halfL, Z: halfT}, // BL top
		{X: +halfW, Y: -halfL, Z: halfT}, // BR top
		{X: +halfW, Y: +halfL, Z: halfT}, // TR top
		{X: -halfW, Y: +halfL, Z: halfT}, // TL top
	}
	var out [4]spatialmath.Pose
	for i, local := range corners {
		localPose := spatialmath.NewPose(local, &spatialmath.OrientationVectorDegrees{OZ: 1})
		composed := spatialmath.Compose(p.pose, localPose)
		out[i] = spatialmath.NewPose(composed.Point(),
			&spatialmath.OrientationVectorDegrees{OZ: -1, Theta: 0})
	}
	return out
}

// statusMap returns the runtime health snapshot. Caller must hold p.mu.
func (p *pallet) statusMap() map[string]interface{} {
	visible := true
	if p.cfg.Visible != nil {
		visible = *p.cfg.Visible
	}
	return map[string]interface{}{
		"ok":          true,
		"model":       PalletModel.String(),
		"name":        p.name.String(),
		"dims_valid":  p.width > 0 && p.length > 0 && p.thickness > 0,
		"color_valid": validateColor(p.color) == nil,
		"visible":     visible,
		"show_axes":   p.cfg.ShowAxes,
	}
}

// summaryString builds a one-line human description. Caller must hold p.mu.
func (p *pallet) summaryString() string {
	label := p.cfg.Label
	if label == "" {
		label = p.name.Name
	}
	pt := p.pose.Point()
	return fmt.Sprintf("pallet %q, %.1fx%.1fx%.1f mm, color (%d,%d,%d), corner at (%.1f, %.1f, %.1f)",
		label, p.width, p.length, p.thickness,
		p.color.R, p.color.G, p.color.B,
		pt.X, pt.Y, pt.Z)
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
	if st, ok := m["style"].(string); ok {
		p.cfg.Style = st
	}
	applyVisualOptions(&p.cfg.VisualOptions, m)
	p.logger.Infow("pallet attributes updated via DoCommand")
	return p.attributesMap(), nil
}

// exchangeState reads the paired tray-dock sensor into the visual's
// terms: is a tray docked, and how far along is the exchange. Returns
// nil (normal pallet) with no dock, a failed read, or readings that do
// not look like a tray-dock's. Takes p.mu only long enough to copy
// config; the sensor RPC runs unlocked and bounded.
func (p *pallet) exchangeState(ctx context.Context) *trayExchangeState {
	p.mu.Lock()
	dock := p.dock
	travel := defaultExchangeTravelMM
	if p.cfg.ExchangeTravelMM > 0 {
		travel = p.cfg.ExchangeTravelMM
	}
	load := defaultExchangeLoadHeightMM
	if h := p.cfg.ExchangeLoadHeightMM; h != nil {
		load = *h
	}
	p.mu.Unlock()

	if dock == nil {
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, sensorReadTimeout)
	defer cancel()
	rd, err := dock.Readings(rctx, nil)
	if err != nil {
		p.noteDockRead(false, err)
		return nil
	}
	present, ok := rd["tray_present"].(bool)
	if !ok {
		p.noteDockRead(false, errNotATrayDock)
		return nil
	}
	p.noteDockRead(true, nil)
	st := &trayExchangeState{
		present:      present,
		travelMM:     travel,
		loadHeightMM: load,
	}
	remaining := asFloat(rd["seconds_until_docked"])
	exchange := asFloat(rd["exchange_seconds"])
	if !st.present && exchange > 0 {
		st.fraction = 1 - remaining/exchange
	}
	return st
}

// noteDockRead logs dock read failures on the ok-to-failing transition
// only. Caller must NOT hold p.mu.
func (p *pallet) noteDockRead(ok bool, err error) {
	p.mu.Lock()
	was := p.dockReadFailing
	p.dockReadFailing = !ok
	p.mu.Unlock()
	if !ok && !was {
		p.logger.Warnw("tray-dock readings failed; tray exchange not "+
			"rendered until reads recover", "error", err)
	}
	if ok && was {
		p.logger.Infow("tray-dock readings recovered")
	}
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
	out := map[string]interface{}{
		"label":        p.cfg.Label,
		"width_mm":     p.width,
		"length_mm":    p.length,
		"thickness_mm": p.thickness,
		"color":        p.color.toMap(),
		"style":        normalizePalletStyle(p.cfg.Style),
		"pose":         poseToWorldMap(p.pose),
		"summary":      p.summaryString(),
	}
	mergeVisualOptions(out, p.cfg.VisualOptions)
	return out
}

// Geometries implements resource.Shaped so the framesystem picks up
// the pallet's footprint for motion-planner collision checks without
// the operator typing a `frame.geometry` block. The geometry is a
// single Box centered at the resource's frame origin (Viam
// convention: frame.translation places a geometry's centroid).
//
// Reads the live width/length/thickness — `set_dimensions` /
// `set_attributes` updates take effect on the next motion plan
// without a reconfigure of dependent modules.
func (p *pallet) Geometries(_ context.Context, _ map[string]any) ([]spatialmath.Geometry, error) {
	p.mu.Lock()
	w, l, t := p.width, p.length, p.thickness
	label := p.cfg.Label
	p.mu.Unlock()
	if label == "" {
		label = "pallet"
	}
	box, err := spatialmath.NewBox(spatialmath.NewZeroPose(), r3.Vector{X: w, Y: l, Z: t}, label)
	if err != nil {
		return nil, fmt.Errorf("pallet Geometries: %w", err)
	}
	return []spatialmath.Geometry{box}, nil
}

// palletSchema returns the webapp-edit schema for the pallet model.
// Geometry attributes scale the visual + collision-bounding box;
// style switches the under-deck composition (stringer/block/plastic).
func palletSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("width_mm", "Width", schemaGroupGeometry, "mm", 50, 5000, 1),
		numEntry("length_mm", "Length", schemaGroupGeometry, "mm", 50, 5000, 1),
		numEntry("thickness_mm", "Thickness", schemaGroupGeometry, "mm", 5, 500, 1),
		enumEntry("style", "Style", schemaGroupGeometry,
			[]string{"stringer", "block", "plastic"}),
		// Plastic pallets render a single slate-grey body — the top-deck
		// color only applies to the slatted stringer/block styles.
		colorEntry("color", "Top-deck color", schemaGroupVisual).whenNot("style", "plastic"),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
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

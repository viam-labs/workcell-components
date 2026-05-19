package workcellcomponents

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/golang/geo/r3"
	"github.com/viam-labs/viamkit/viz"
	commonpb "go.viam.com/api/common/v1"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/worldstatestore"
	"go.viam.com/rdk/spatialmath"
)

// WorkcellSceneModel is the rdk:service:world_state_store model that
// publishes pallet and pick-station visuals as colored Box transforms
// to the Viam 3D scene viewer. Bridges the gap that this RDK release
// leaves open: `resource.Shaped` (which 0.3.0+ added) reaches the
// motion planner but NOT the scene renderer. With a `workcell-scene`
// service in the cell config, the pallet and pick-station finally
// render visually — colored boxes at their configured poses, dims
// pulled from each component's live state via DoCommand.
//
// The service polls each configured component every `tick_interval_secs`
// (default 1.0) and republishes when something changes. Pose and
// dimension changes emit UPDATED (the renderer honors them). Color
// changes emit REMOVED+ADDED with a rotated UUID — the renderer
// silently drops metadata.* updates on plain UPDATE (see the
// renderer-update-path-matcher memory note), so the UUID rotation is
// the only reliable way to propagate a color change.
//
// Config (all fields optional):
//
//	{
//	  "pallet_names":       ["pallet"],
//	  "pick_station_names": ["pick-station"],
//	  "tick_interval_secs": 1.0
//	}
//
// Components named in the config must be present in the cell config
// before this service constructs (they're declared as required
// dependencies via Validate).
var WorkcellSceneModel = resource.NewModel("viam", "workcell-components", "workcell-scene")

const (
	defaultSceneTickIntervalSecs = 1.0
	sceneSubscriberBufferSize    = 128
)

// WorkcellSceneConfig is the persisted attribute shape.
type WorkcellSceneConfig struct {
	// PalletNames are the resource names of pallet components this
	// scene should publish.
	PalletNames []string `json:"pallet_names,omitempty"`

	// PickStationNames are the resource names of pick-station
	// components this scene should publish.
	PickStationNames []string `json:"pick_station_names,omitempty"`

	// TickIntervalSecs is the poll interval for republishing.
	// Defaults to 1.0; values <0.2 are clamped up to avoid hammering
	// the sibling components' DoCommand surfaces.
	TickIntervalSecs float64 `json:"tick_interval_secs,omitempty"`
}

func (c *WorkcellSceneConfig) Validate(_ string) ([]string, []string, error) {
	// All declared components are required deps so the resource
	// manager constructs them before us and we can call DoCommand.
	deps := make([]string, 0, len(c.PalletNames)+len(c.PickStationNames))
	for _, n := range c.PalletNames {
		deps = append(deps, generic.Named(n).String())
	}
	for _, n := range c.PickStationNames {
		deps = append(deps, generic.Named(n).String())
	}
	return deps, nil, nil
}

func init() {
	resource.RegisterService(worldstatestore.API, WorkcellSceneModel,
		resource.Registration[worldstatestore.Service, *WorkcellSceneConfig]{
			Constructor: newWorkcellScene,
		},
	)
}

type workcellScene struct {
	resource.AlwaysRebuild

	name   resource.Name
	logger logging.Logger
	cfg    WorkcellSceneConfig

	// Component handles — resolved at construction. AlwaysRebuild
	// cascades on dependency changes, so these stay current when a
	// pallet or pick-station is reconfigured.
	pallets      map[string]resource.Resource // name → component
	pickStations map[string]resource.Resource

	// Store-of-transforms with WSS plumbing wired in. We Set into it
	// on construction + every tick; subscribers get the events via
	// the embedded StreamTransformChanges path.
	store *viz.Store

	// versions tracks the UUID rotation counter per component. We
	// rotate UUIDs only on color changes (the renderer drops
	// metadata.* updates silently); pose/dim changes go through plain
	// UPDATE via store.Set with the same UUID.
	mu       sync.Mutex
	versions map[string]int
	prevKey  map[string]sceneEntryKey // name → last-seen identity for diff

	// Lifecycle for the tick goroutine.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// sceneEntryKey is the per-component prior snapshot used for change
// detection. Color changes drive a UUID rotation; pose+dim changes
// drive a plain Set (which Store emits as UPDATED).
type sceneEntryKey struct {
	col  Color
	uuid string
}

// sceneSnapshot is the per-component live data fetched on each tick.
type sceneSnapshot struct {
	name      string
	uuid      string
	pose      spatialmath.Pose
	width     float64
	length    float64
	thickness float64
	color     Color
}

func newWorkcellScene(
	ctx context.Context,
	deps resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (worldstatestore.Service, error) {
	cfg, err := resource.NativeConfig[*WorkcellSceneConfig](conf)
	if err != nil {
		return nil, err
	}

	tick := cfg.TickIntervalSecs
	if tick <= 0 {
		tick = defaultSceneTickIntervalSecs
	}
	if tick < 0.2 {
		tick = 0.2 // clamp — don't hammer sibling DoCommand
	}

	s := &workcellScene{
		name:         conf.ResourceName(),
		logger:       logger,
		cfg:          *cfg,
		pallets:      map[string]resource.Resource{},
		pickStations: map[string]resource.Resource{},
		store: viz.NewStore(
			viz.WithChangeBufferSize(sceneSubscriberBufferSize),
			viz.OnDropped(func(uuid string) {
				logger.Warnw("workcell-scene: dropped change event (subscriber buffer full)", "uuid", uuid)
			}),
		),
		versions: map[string]int{},
		prevKey:  map[string]sceneEntryKey{},
	}

	// Resolve component dependencies.
	for _, name := range cfg.PalletNames {
		r, err := resource.FromDependencies[resource.Resource](deps, generic.Named(name))
		if err != nil {
			return nil, fmt.Errorf("pallet %q: %w", name, err)
		}
		s.pallets[name] = r
	}
	for _, name := range cfg.PickStationNames {
		r, err := resource.FromDependencies[resource.Resource](deps, generic.Named(name))
		if err != nil {
			return nil, fmt.Errorf("pick-station %q: %w", name, err)
		}
		s.pickStations[name] = r
	}

	// Initial publish — populate the store so renderer connects see
	// the slabs immediately. Don't fail construction on transient
	// component errors; tick loop will retry.
	if err := s.refresh(ctx); err != nil {
		logger.Warnw("workcell-scene: initial refresh failed", "error", err)
	}

	// Start the tick goroutine.
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	tickInterval := time.Duration(tick * float64(time.Second))
	go s.tickLoop(tickInterval)

	logger.Infow("workcell-scene started",
		"pallets", cfg.PalletNames,
		"pick_stations", cfg.PickStationNames,
		"tick_interval", tickInterval,
	)
	return s, nil
}

// Close stops the tick goroutine. Required for the resource.Resource
// interface (worldstatestore.Service embeds it).
func (s *workcellScene) Close(_ context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return nil
}

func (s *workcellScene) Name() resource.Name { return s.name }

// ListUUIDs implements worldstatestore.Service via the embedded Store.
func (s *workcellScene) ListUUIDs(ctx context.Context, extra map[string]any) ([][]byte, error) {
	return s.store.ListUUIDs(ctx, extra)
}

// GetTransform implements worldstatestore.Service via the embedded Store.
func (s *workcellScene) GetTransform(ctx context.Context, uuid []byte, extra map[string]any) (*commonpb.Transform, error) {
	return s.store.GetTransform(ctx, uuid, extra)
}

// StreamTransformChanges implements worldstatestore.Service via the
// embedded Store.
func (s *workcellScene) StreamTransformChanges(ctx context.Context, extra map[string]any) (*worldstatestore.TransformChangeStream, error) {
	return s.store.StreamTransformChanges(ctx, extra)
}

// DoCommand exposes diagnostic verbs for the operator UI:
//
//	{"refresh": true}  → manual re-poll of all components (returns
//	                     {"refreshed": N})
//	{"len":     true}  → number of transforms currently published
//	{"list":    true}  → {"uuids": [...]} list of current transform UUIDs
func (s *workcellScene) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	if _, ok := cmd["refresh"]; ok {
		if err := s.refresh(ctx); err != nil {
			return nil, err
		}
		return map[string]interface{}{"refreshed": s.store.Len()}, nil
	}
	if _, ok := cmd["len"]; ok {
		return map[string]interface{}{"count": s.store.Len()}, nil
	}
	if _, ok := cmd["list"]; ok {
		uuids, err := s.store.ListUUIDs(ctx, nil)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(uuids))
		for _, u := range uuids {
			out = append(out, string(u))
		}
		return map[string]interface{}{"uuids": out}, nil
	}
	return nil, fmt.Errorf("workcell-scene: unknown command %v", cmd)
}

// tickLoop polls each component on a ticker and republishes when
// anything changes. Exits on context cancel.
func (s *workcellScene) tickLoop(interval time.Duration) {
	defer s.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.refresh(s.ctx); err != nil {
				s.logger.Warnw("workcell-scene tick: refresh failed", "error", err)
			}
		}
	}
}

// refresh polls every configured component, builds a viz.Box per
// component, and pushes changes through the store.
func (s *workcellScene) refresh(ctx context.Context) error {
	snaps := make([]sceneSnapshot, 0, len(s.pallets)+len(s.pickStations))

	for name, r := range s.pallets {
		snap, err := s.pollComponent(ctx, name, r, "pallet")
		if err != nil {
			s.logger.Warnw("workcell-scene: pallet poll failed", "name", name, "error", err)
			continue
		}
		snaps = append(snaps, snap)
	}
	for name, r := range s.pickStations {
		snap, err := s.pollComponent(ctx, name, r, "pick-station")
		if err != nil {
			s.logger.Warnw("workcell-scene: pick-station poll failed", "name", name, "error", err)
			continue
		}
		snaps = append(snaps, snap)
	}

	for _, snap := range snaps {
		s.applySnapshot(snap)
	}
	return nil
}

// pollComponent issues two DoCommand calls — one for get_visual_pose
// (the centroid pose suitable for a viz.Box) and one for get_attributes
// (dims + color) — and returns the snapshot.
func (s *workcellScene) pollComponent(ctx context.Context, name string, r resource.Resource, kind string) (sceneSnapshot, error) {
	poseResp, err := r.DoCommand(ctx, map[string]interface{}{"get_visual_pose": true})
	if err != nil {
		return sceneSnapshot{}, fmt.Errorf("get_visual_pose: %w", err)
	}
	attrResp, err := r.DoCommand(ctx, map[string]interface{}{"get_attributes": true})
	if err != nil {
		return sceneSnapshot{}, fmt.Errorf("get_attributes: %w", err)
	}

	pose := poseFromAttrMap(poseResp)
	width := asFloat(attrResp["width_mm"])
	length := asFloat(attrResp["length_mm"])
	thickness := asFloat(attrResp["thickness_mm"])
	color := colorFromAttrMap(attrResp["color"])

	if width <= 0 || length <= 0 || thickness <= 0 {
		return sceneSnapshot{}, fmt.Errorf("%s %q: invalid dims (%vx%vx%v)", kind, name, width, length, thickness)
	}

	s.mu.Lock()
	uuid := s.uuidForLocked(name, color)
	s.mu.Unlock()

	return sceneSnapshot{
		name:      name,
		uuid:      uuid,
		pose:      pose,
		width:     width,
		length:    length,
		thickness: thickness,
		color:     color,
	}, nil
}

// applySnapshot diffs against the prior snapshot for this name and
// emits the appropriate event(s) through the store. Color changes
// trigger a UUID rotation (REMOVED old + ADDED new); pose/dim
// changes go through plain Set (UPDATED) with the same UUID.
func (s *workcellScene) applySnapshot(snap sceneSnapshot) {
	s.mu.Lock()
	prev, hadPrev := s.prevKey[snap.name]
	colorChanged := hadPrev && prev.col != snap.color
	if colorChanged {
		oldUUID := prev.uuid
		s.prevKey[snap.name] = sceneEntryKey{col: snap.color, uuid: snap.uuid}
		s.mu.Unlock()

		s.store.Remove(oldUUID)
		s.store.Set(s.buildTransform(snap))
		return
	}
	s.prevKey[snap.name] = sceneEntryKey{col: snap.color, uuid: snap.uuid}
	s.mu.Unlock()

	// Pose/dim change OR initial add — Set will emit ADDED or UPDATED
	// as appropriate (Store keys by UUID; new UUID = ADDED, same UUID
	// = UPDATED).
	s.store.Set(s.buildTransform(snap))
}

func (s *workcellScene) buildTransform(snap sceneSnapshot) *commonpb.Transform {
	c := viz.Color{R: snap.color.R, G: snap.color.G, B: snap.color.B, Opacity: snap.color.effectiveOpacity()}
	return viz.Box{
		UUID:          snap.uuid,
		ObserverFrame: "world",
		Pose:          snap.pose,
		DimsMM:        r3.Vector{X: snap.width, Y: snap.length, Z: snap.thickness},
		Color:         c,
		Label:         snap.name,
	}.ToTransform()
}

// uuidForLocked returns the current UUID for a component, rotating
// it if the color has changed since the last snapshot. Caller must
// hold s.mu.
func (s *workcellScene) uuidForLocked(name string, color Color) string {
	prev, ok := s.prevKey[name]
	if !ok {
		// First time we've seen this component — uuid = name.
		return name
	}
	if prev.col == color {
		// No color change; keep the prior UUID.
		return prev.uuid
	}
	// Color changed — rotate.
	s.versions[name]++
	return fmt.Sprintf("%s-v%d", name, s.versions[name])
}

// --- helpers -----------------------------------------------------------

// poseFromAttrMap reconstructs a spatialmath.Pose from the
// {x,y,z,o_x,o_y,o_z,theta} shape returned by get_pose/get_visual_pose.
func poseFromAttrMap(m map[string]interface{}) spatialmath.Pose {
	return spatialmath.NewPose(
		r3.Vector{X: asFloat(m["x"]), Y: asFloat(m["y"]), Z: asFloat(m["z"])},
		&spatialmath.OrientationVectorDegrees{
			OX: asFloat(m["o_x"]), OY: asFloat(m["o_y"]),
			OZ: asFloat(m["o_z"]), Theta: asFloat(m["theta"]),
		},
	)
}

// colorFromAttrMap reads a Color out of an attributes map's "color"
// sub-object. Returns zero Color if missing.
func colorFromAttrMap(v interface{}) Color {
	m, ok := v.(map[string]interface{})
	if !ok {
		return Color{}
	}
	return Color{
		R: int(asFloat(m["r"])),
		G: int(asFloat(m["g"])),
		B: int(asFloat(m["b"])),
		A: asFloat(m["opacity"]),
	}
}

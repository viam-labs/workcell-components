package workcellcomponents

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	visuals "github.com/viam-labs/viam-viz-helpers-go"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/worldstatestore"
)

// WorkcellSceneModel is the rdk:service:world_state_store model that
// publishes the workcell's components (pallet, pick-station, and any
// affordance components added later) as composed visuals to the Viam
// 3D scene viewer.
//
// The service polls each configured component every
// `tick_interval_secs` (default 1.0) and republishes when something
// changes. It builds nothing locally: every visual entry comes from a
// sibling component's `get_visuals` DoCommand response, which lets the
// scene service stay type-agnostic — adding a new affordance component
// requires zero changes here.
//
// Built on top of github.com/viam-labs/viam-viz-helpers-go: SceneServiceBase
// handles the WSS gRPC plumbing (ListUUIDs / GetTransform /
// StreamTransformChanges), the subscriber broadcast, the animation
// tick loop, the standard DoCommand verbs (list / clear / snapshot /
// apply_events), and the renderer's metadata-only-update quirk (the
// library transparently issues REMOVE + re-ADD with a fresh UUID for
// color / opacity changes — see [[feedback_renderer_update_path_matcher]]).
//
// Config (all fields optional):
//
//	{
//	  "component_names":    ["pallet", "pick-station", "fence-north", ...],
//	  "pallet_names":       ["pallet"],         // deprecated alias
//	  "pick_station_names": ["pick-station"],   // deprecated alias
//	  "tick_interval_secs": 1.0
//	}
//
// Components named here must be present in the cell config and must
// implement `get_visuals` via DoCommand (returns `{"visuals": [...]}`
// — see visuals_wire.go for the entry shape).
var WorkcellSceneModel = resource.NewModel("viam", "workcell-components", "workcell-scene")

const (
	defaultSceneTickIntervalSecs = 1.0
	minSceneTickIntervalSecs     = 0.1 // clamp — don't hammer sibling DoCommand
	defaultParentFrame           = "world"
	defaultAnimationTickHz       = 30.0
)

// WorkcellSceneConfig is the persisted attribute shape.
type WorkcellSceneConfig struct {
	// ComponentNames lists the sibling generic components this scene
	// publishes. Each must expose a `get_visuals` DoCommand verb.
	ComponentNames []string `json:"component_names,omitempty"`

	// PalletNames / PickStationNames are deprecated typed aliases —
	// merged into ComponentNames at Validate time. Existing configs
	// keep working; new configs should use ComponentNames.
	PalletNames      []string `json:"pallet_names,omitempty"`
	PickStationNames []string `json:"pick_station_names,omitempty"`

	// TickIntervalSecs is the poll interval for republishing.
	// Defaults to 1.0; clamped to [0.1, ∞).
	TickIntervalSecs float64 `json:"tick_interval_secs,omitempty"`
}

// allComponentNames returns the deduplicated union of ComponentNames
// + the deprecated typed aliases, preserving the order of first
// occurrence.
func (c *WorkcellSceneConfig) allComponentNames() []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, n := range c.ComponentNames {
		add(n)
	}
	for _, n := range c.PalletNames {
		add(n)
	}
	for _, n := range c.PickStationNames {
		add(n)
	}
	return out
}

func (c *WorkcellSceneConfig) Validate(_ string) ([]string, []string, error) {
	names := c.allComponentNames()
	deps := make([]string, 0, len(names))
	for _, n := range names {
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

// workcellScene polls its configured sibling components every tick,
// translates each component's `get_visuals` response into typed
// visuals.Visual entries, and pushes the resulting deltas through
// the embedded SceneServiceBase to subscribers (the 3D viewer).
//
// The visuals library tick loop runs in parallel on a faster cadence
// (30 Hz default) to dispatch any per-Visual Animation specs the
// components attached. Animation and poll updates share s.scene via
// s.sceneMu.
type workcellScene struct {
	resource.Named
	resource.AlwaysRebuild
	visuals.SceneServiceBase

	logger logging.Logger
	cfg    WorkcellSceneConfig

	// Component handles — resolved at construction. AlwaysRebuild
	// cascades on dependency changes, so these stay current when a
	// sibling component is reconfigured.
	sources map[string]resource.Resource // name → component

	// sceneMu serializes access to s.SceneServiceBase.Scene between
	// the poll loop (refresh — AddOrUpdate from sibling get_visuals)
	// and the library's animation tick loop (SceneTick — mutates
	// Visual pointers and calls scene.Update).
	sceneMu sync.Mutex

	// Lifecycle for the poll goroutine.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
	if tick < minSceneTickIntervalSecs {
		tick = minSceneTickIntervalSecs
	}

	s := &workcellScene{
		Named:   conf.ResourceName().AsNamed(),
		logger:  logger,
		cfg:     *cfg,
		sources: map[string]resource.Resource{},
	}
	s.SceneServiceBase.Logger = logger
	s.SceneServiceBase.DefaultParentFrame = defaultParentFrame
	s.SceneServiceBase.DefaultTickHz = defaultAnimationTickHz
	s.SceneServiceBase.DefaultUUIDStrategy = "stable"
	// Hooks = s so the library calls SceneTick on us; lets the library's
	// animation tick loop drive Animation specs attached to component
	// visuals (e.g. roller spin, stack-light flash).
	s.SceneServiceBase.Hooks = s

	// Resolve component dependencies.
	for _, name := range cfg.allComponentNames() {
		r, err := resource.FromDependencies[resource.Resource](deps, generic.Named(name))
		if err != nil {
			return nil, fmt.Errorf("component %q: %w", name, err)
		}
		s.sources[name] = r
	}

	// Initial poll — populate the scene so subscribers connecting
	// immediately after construction see the workcell, not an empty
	// world. Don't fail construction on transient errors; the poll
	// loop will retry.
	//
	// SetScene (not ReconfigureWith) is critical — it sets
	// s.SceneServiceBase.Scene + baseVisuals, which the library's
	// animation tick loop requires to dispatch Spin / Pulse / Flicker
	// animations on Visuals returned by components.
	initialVisuals := s.collectInitialVisuals(ctx)
	if err := s.SceneServiceBase.SetScene(
		visuals.SetSceneOpts{
			TickHz:       defaultAnimationTickHz,
			UUIDStrategy: "stable",
			ParentFrame:  defaultParentFrame,
		},
		visualsAsInterfaceSlice(initialVisuals)...,
	); err != nil {
		return nil, fmt.Errorf("workcell-scene: SetScene: %w", err)
	}

	// Start the poll goroutine.
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go s.pollLoop(time.Duration(tick * float64(time.Second)))

	logger.Infow("workcell-scene started",
		"components", cfg.allComponentNames(),
		"tick_interval_secs", tick,
		"initial_visuals", len(initialVisuals),
	)
	return s, nil
}

// SceneTick implements visuals.SceneTicker. Delegates to the library's
// DefaultSceneTick so any Visual whose Animation field is set (Spin,
// Pulse, Flicker, ...) ticks automatically. Components contribute
// animations by attaching them in their `get_visuals` response.
func (s *workcellScene) SceneTick(scene *visuals.Scene, t float64) []visuals.SceneEvent {
	s.sceneMu.Lock()
	defer s.sceneMu.Unlock()
	return s.SceneServiceBase.DefaultSceneTick(scene, t)
}

// Close stops the poll goroutine and the SceneServiceBase tick loop.
func (s *workcellScene) Close(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return s.SceneServiceBase.Close(ctx)
}

// DoCommand disambiguates between resource.Named's DoCommand and
// SceneServiceBase.DoCommand, then adds our own `refresh` verb for
// the operator UI.
func (s *workcellScene) DoCommand(ctx context.Context, cmd map[string]any) (map[string]any, error) {
	if _, ok := cmd["refresh"]; ok {
		s.refresh(ctx)
		return map[string]any{"refreshed": s.SceneServiceBase.State() != nil}, nil
	}
	return s.SceneServiceBase.DoCommand(ctx, cmd)
}

// pollLoop polls every component on a ticker and republishes changes.
// Exits on context cancel.
func (s *workcellScene) pollLoop(interval time.Duration) {
	defer s.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.refresh(s.ctx)
		}
	}
}

// refresh polls every configured component, builds the new Visual set,
// diffs it against the library-managed Scene, and pushes the resulting
// events through SceneServiceBase.apply_events for broadcast.
//
// Operates on s.SceneServiceBase.Scene (populated by SetScene) rather
// than a private Scene — keeps animation dispatch live: the library's
// tick loop reads from the same Scene and mutates Visual pointers in
// place, so a poll-driven pose change (operator moved a sibling) and
// a tick-driven animation (roller spin) stack cleanly.
//
// Also reaps orphan labels — when a component's get_visuals response
// stops including a label (style switch, visibility toggle, count
// change), the label is removed from the scene. Reaping only happens
// for labels whose owning component polled SUCCESSFULLY this tick; a
// transient DoCommand failure leaves the prior labels in place.
func (s *workcellScene) refresh(ctx context.Context) {
	results := s.pollAllComponents(ctx)

	// Build set of (owner -> polled-OK) and (label -> present) for
	// the orphan-reaping step. Owner = label prefix before "/" (every
	// visual builder in this module namespaces its output as
	// "{component-name}/{kind-id}").
	polledOK := make(map[string]bool, len(results))
	presentLabels := make(map[string]bool)
	var newVisuals []visuals.Visual
	for _, r := range results {
		if r.err != nil {
			continue
		}
		polledOK[r.componentName] = true
		for _, v := range r.visuals {
			presentLabels[v.ToItem().Label] = true
			newVisuals = append(newVisuals, v)
		}
	}

	s.sceneMu.Lock()
	defer s.sceneMu.Unlock()

	if s.SceneServiceBase.Scene == nil {
		// SetScene never ran — shouldn't happen, but guard against
		// nil-deref before the animation tick loop catches up.
		return
	}

	// Reap orphans: labels currently in the scene whose OWNER polled
	// successfully this tick but didn't include them in the response.
	var orphans []string
	for _, label := range s.SceneServiceBase.Scene.Labels() {
		if presentLabels[label] {
			continue
		}
		owner, ok := ownerOfLabel(label)
		if !ok {
			continue // unnamespaced — leave alone
		}
		if !polledOK[owner] {
			continue // owner's poll failed this tick — keep its labels
		}
		orphans = append(orphans, label)
	}

	var allEvents []visuals.SceneEvent
	for _, label := range orphans {
		allEvents = append(allEvents, s.SceneServiceBase.Scene.Remove(label)...)
	}
	if len(newVisuals) > 0 {
		events, err := s.SceneServiceBase.Scene.AddOrUpdate(visualsAsInterfaceSlice(newVisuals)...)
		if err != nil {
			s.logger.Warnw("workcell-scene: AddOrUpdate failed", "error", err)
		} else {
			allEvents = append(allEvents, events...)
		}
	}

	if len(allEvents) == 0 {
		return
	}

	wire := visuals.EventsToWire(allEvents)
	wireAny := make([]any, len(wire))
	for i, w := range wire {
		wireAny[i] = w
	}
	if _, err := s.SceneServiceBase.DoCommand(ctx, map[string]any{
		"command": "apply_events",
		"events":  wireAny,
	}); err != nil {
		s.logger.Warnw("workcell-scene: apply_events failed", "error", err)
	}
}

// ownerOfLabel returns the prefix before the first "/" in a visual
// label — the convention `{component-name}/{kind-id}` every visual
// builder in this module uses. Returns ok=false for labels without
// a "/" so reaping skips them (defensive — third-party components
// might emit unnamespaced labels).
func ownerOfLabel(label string) (string, bool) {
	if i := strings.Index(label, "/"); i > 0 {
		return label[:i], true
	}
	return "", false
}

// pollResult captures one component's get_visuals outcome — the
// parsed Visual list when polling succeeded, or err non-nil when it
// failed. The orphan-reaping step uses err to decide whether to trust
// the absence of a label.
type pollResult struct {
	componentName string
	visuals       []visuals.Visual
	err           error
}

// pollAllComponents polls each configured component's get_visuals
// DoCommand verb in sequence and returns the parsed results.
func (s *workcellScene) pollAllComponents(ctx context.Context) []pollResult {
	results := make([]pollResult, 0, len(s.sources))
	for name, r := range s.sources {
		resp, err := r.DoCommand(ctx, map[string]interface{}{"get_visuals": true})
		if err != nil {
			s.logger.Warnw("workcell-scene: get_visuals failed", "name", name, "error", err)
			results = append(results, pollResult{componentName: name, err: err})
			continue
		}
		entries := coerceWireVisualsSlice(resp["visuals"])
		vs := make([]visuals.Visual, 0, len(entries))
		for i, m := range entries {
			v, parseErr := wireToVisual(m)
			if parseErr != nil {
				s.logger.Warnw("workcell-scene: wireToVisual failed",
					"name", name, "index", i, "error", parseErr)
				continue
			}
			vs = append(vs, v)
		}
		results = append(results, pollResult{componentName: name, visuals: vs})
	}
	return results
}

// collectInitialVisuals issues `get_visuals` to every configured
// component for the SetScene call at construction. Errors on a single
// component are logged and skipped — one broken sibling shouldn't
// prevent the rest of the workcell from rendering. The poll loop
// retries via pollAllComponents.
func (s *workcellScene) collectInitialVisuals(ctx context.Context) []visuals.Visual {
	out := make([]visuals.Visual, 0, len(s.sources))
	for _, r := range s.pollAllComponents(ctx) {
		if r.err != nil {
			continue
		}
		out = append(out, r.visuals...)
	}
	return out
}

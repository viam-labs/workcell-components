package workcellcomponents

import (
	"context"
	"strings"
	"testing"

	"github.com/golang/geo/r3"
	visuals "github.com/viam-labs/viam-viz-helpers-go"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

// Wire format must round-trip: a visualWire → map → wireToVisual
// produces a typed visuals.Visual whose key fields match the input.
func TestVisualWireRoundTrip_Box(t *testing.T) {
	w := visualWire{
		Type:        "box",
		Label:       "pallet/slat-0",
		ParentFrame: "world",
		Pose:        &visualPoseWire{X: 100, Y: 200, Z: 50, OZ: 1},
		DimsMM:      &visualDimsWire{X: 1000, Y: 100, Z: 18},
		Color:       &visualColorWire{R: 198, G: 153, B: 97, Opacity: 1},
	}
	m, err := visualWireToMap(w)
	if err != nil {
		t.Fatalf("visualWireToMap: %v", err)
	}
	v, err := wireToVisual(m)
	if err != nil {
		t.Fatalf("wireToVisual: %v", err)
	}
	box, ok := v.(*visuals.Box)
	if !ok {
		t.Fatalf("expected *visuals.Box, got %T", v)
	}
	if box.Label != "pallet/slat-0" {
		t.Errorf("label: got %q, want %q", box.Label, "pallet/slat-0")
	}
	if box.DimsMM.X != 1000 || box.DimsMM.Y != 100 || box.DimsMM.Z != 18 {
		t.Errorf("dims: got %+v, want {1000,100,18}", box.DimsMM)
	}
	if box.Color == nil || box.Color.R != 198 {
		t.Errorf("color: got %+v, want R=198", box.Color)
	}
	if box.Pose.X != 100 || box.Pose.Y != 200 || box.Pose.Z != 50 {
		t.Errorf("pose: got %+v, want X=100 Y=200 Z=50", box.Pose)
	}
}

func TestVisualWireRoundTrip_Capsule(t *testing.T) {
	w := visualWire{
		Type:     "capsule",
		Label:    "pick-station/roller-00",
		Pose:     &visualPoseWire{OX: 1},
		RadiusMM: 12,
		LengthMM: 380,
		Color:    &visualColorWire{R: 180, G: 184, B: 190},
	}
	m, _ := visualWireToMap(w)
	v, err := wireToVisual(m)
	if err != nil {
		t.Fatalf("wireToVisual: %v", err)
	}
	cap, ok := v.(*visuals.Capsule)
	if !ok {
		t.Fatalf("expected *visuals.Capsule, got %T", v)
	}
	if cap.RadiusMM != 12 || cap.LengthMM != 380 {
		t.Errorf("dims: got R=%v L=%v, want R=12 L=380", cap.RadiusMM, cap.LengthMM)
	}
}

func TestVisualWireRoundTrip_Arrow(t *testing.T) {
	w := visualWire{
		Type:     "arrow",
		Label:    "pick-station/direction-arrow",
		Pose:     &visualPoseWire{OY: 1},
		RadiusMM: 8,
		LengthMM: 200,
	}
	m, _ := visualWireToMap(w)
	v, err := wireToVisual(m)
	if err != nil {
		t.Fatalf("wireToVisual: %v", err)
	}
	arr, ok := v.(*visuals.Arrow)
	if !ok {
		t.Fatalf("expected *visuals.Arrow, got %T", v)
	}
	if arr.RadiusMM != 8 || arr.LengthMM != 200 {
		t.Errorf("dims: got R=%v L=%v, want R=8 L=200", arr.RadiusMM, arr.LengthMM)
	}
}

// The whole point of grouping: re-composing the anchor with each
// child's reparented local pose must land back on the child's
// original world pose. A direction error in PoseBetween would put
// every child at the mirrored offset — tests that only check
// parent_frame strings wouldn't notice. This locks the math down.
func TestGroupUnderFrame_RecomposesToWorldPose(t *testing.T) {
	// Anchor with a non-trivial position AND rotation, so a bad
	// inverse would visibly diverge.
	anchor := spatialmath.NewPose(
		r3.Vector{X: 850, Y: 100, Z: 76},
		&spatialmath.OrientationVectorDegrees{OZ: 1, Theta: 35},
	)
	childWorlds := []spatialmath.Pose{
		spatialmath.NewPose(r3.Vector{X: 900, Y: 150, Z: 200},
			&spatialmath.OrientationVectorDegrees{OZ: 1}),
		spatialmath.NewPose(r3.Vector{X: 700, Y: -300, Z: 40},
			&spatialmath.OrientationVectorDegrees{OX: 1}),
		spatialmath.NewPose(r3.Vector{X: 850, Y: 100, Z: 500},
			&spatialmath.OrientationVectorDegrees{OY: 1, Theta: 90}),
	}
	children := make([]visualWire, len(childWorlds))
	for i, cw := range childWorlds {
		children[i] = visualWire{
			Type:   "box",
			Label:  "thing/part",
			Pose:   poseToWire(cw),
			DimsMM: &visualDimsWire{X: 10, Y: 10, Z: 10},
		}
	}

	grouped := groupUnderFrame("thing", anchor, false, children)
	if len(grouped) != len(children)+1 {
		t.Fatalf("got %d entries, want %d (frame + children)", len(grouped), len(children)+1)
	}
	// grouped[0] is the anchor frame; grouped[1:] are the children.
	for i, cw := range childWorlds {
		childLocal := wirePoseToSpatial(grouped[i+1].Pose)
		recomposed := spatialmath.Compose(anchor, childLocal)
		if !spatialmath.PoseAlmostEqual(recomposed, cw) {
			t.Errorf("child %d: recomposed %v (orient %+v) != original world %v (orient %+v)",
				i, recomposed.Point(), recomposed.Orientation().OrientationVectorDegrees(),
				cw.Point(), cw.Orientation().OrientationVectorDegrees())
		}
	}
}

// A "frame" wire entry must translate to a *visuals.Frame anchor.
func TestVisualWireRoundTrip_Frame(t *testing.T) {
	w := visualWire{
		Type:           "frame",
		Label:          "pallet/group",
		ParentFrame:    "world",
		Pose:           &visualPoseWire{X: 850, Z: 76, OZ: 1},
		ShowAxesHelper: true,
	}
	m, _ := visualWireToMap(w)
	v, err := wireToVisual(m)
	if err != nil {
		t.Fatalf("wireToVisual: %v", err)
	}
	fr, ok := v.(*visuals.Frame)
	if !ok {
		t.Fatalf("expected *visuals.Frame, got %T", v)
	}
	if fr.Label != "pallet/group" {
		t.Errorf("label: got %q, want \"pallet/group\"", fr.Label)
	}
	if fr.ParentFrame != "world" {
		t.Errorf("parent: got %q, want \"world\"", fr.ParentFrame)
	}
	if fr.HideAxes {
		t.Errorf("HideAxes = true, want false (show_axes_helper was true)")
	}
}

// wireToVisual must reject malformed entries instead of panicking —
// a single bad component shouldn't kill the scene service.
func TestWireToVisual_RejectsMissingLabel(t *testing.T) {
	_, err := wireToVisual(map[string]interface{}{"type": "box"})
	if err == nil {
		t.Errorf("expected error for missing label")
	}
}

func TestWireToVisual_RejectsBadType(t *testing.T) {
	_, err := wireToVisual(map[string]interface{}{
		"type": "cube", "label": "x",
	})
	if err == nil {
		t.Errorf("expected error for unknown type 'cube'")
	}
}

func TestWireToVisual_RejectsBoxWithoutDims(t *testing.T) {
	_, err := wireToVisual(map[string]interface{}{
		"type": "box", "label": "x",
	})
	if err == nil {
		t.Errorf("expected error for box without dims_mm")
	}
}

// coerceWireVisualsSlice must handle both in-process ([]map[string]any)
// and gRPC-erased ([]any) shapes — same convention as the library's
// coerceEventsSlice.
func TestCoerceWireVisualsSlice_HandlesBothShapes(t *testing.T) {
	// In-process Go shape.
	got := coerceWireVisualsSlice([]map[string]interface{}{
		{"type": "box", "label": "a"},
		{"type": "sphere", "label": "b"},
	})
	if len(got) != 2 {
		t.Errorf("in-process shape: got %d entries, want 2", len(got))
	}

	// gRPC-erased shape.
	got = coerceWireVisualsSlice([]interface{}{
		map[string]interface{}{"type": "box", "label": "a"},
		map[string]interface{}{"type": "sphere", "label": "b"},
	})
	if len(got) != 2 {
		t.Errorf("gRPC shape: got %d entries, want 2", len(got))
	}
}

// Pallet's get_visuals returns a multi-primitive composite (Phase B).
// At minimum: top-deck slats + stringers + bottom-deck boards.
func TestPalletGetVisuals_ReturnsMultiPrimitive(t *testing.T) {
	p := newTestPallet(t)
	resp, err := p.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	if err != nil {
		t.Fatalf("get_visuals: %v", err)
	}
	entries, ok := resp["visuals"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map, got %T", resp["visuals"])
	}
	// Standard GMA dimensions produce ~7 top slats + 3 stringers +
	// ~3 bottom boards — the smaller bound is conservative enough
	// to survive future adaptive-count tuning.
	if len(entries) < 8 {
		t.Errorf("got %d visual entries on a GMA-sized pallet, want at least 8",
			len(entries))
	}
	// Spot-check label patterns.
	labels := visualLabels(entries)
	mustContain(t, labels, "slat-0")
	mustContain(t, labels, "stringer-0")
	mustContain(t, labels, "bottom-board-0")
}

// Pallet block style should produce 9 blocks instead of 3 stringers.
func TestPalletGetVisuals_BlockStyle(t *testing.T) {
	p := newTestPallet(t)
	// Switch to block style via DoCommand.
	_, err := p.DoCommand(context.Background(), map[string]interface{}{
		"set_attributes": map[string]interface{}{"style": "block"},
	})
	if err != nil {
		t.Fatalf("set_attributes style=block: %v", err)
	}
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	entries, _ := resp["visuals"].([]map[string]interface{})
	labels := visualLabels(entries)
	// Should have block-0-0 through block-2-2 (9 blocks).
	mustContain(t, labels, "block-0-0")
	mustContain(t, labels, "block-2-2")
	mustNotContain(t, labels, "stringer-0")
}

// Pallet plastic style should produce a single body box, plus the
// parent anchor frame every component now emits.
func TestPalletGetVisuals_PlasticStyle(t *testing.T) {
	p := newTestPallet(t)
	_, err := p.DoCommand(context.Background(), map[string]interface{}{
		"set_attributes": map[string]interface{}{"style": "plastic"},
	})
	if err != nil {
		t.Fatalf("set_attributes style=plastic: %v", err)
	}
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	entries, _ := resp["visuals"].([]map[string]interface{})
	if len(entries) != 2 {
		t.Errorf("plastic style: got %d entries, want 2 (anchor frame + body box)", len(entries))
	}
	types := map[string]int{}
	for _, e := range entries {
		ty, _ := e["type"].(string)
		types[ty]++
	}
	if types["frame"] != 1 || types["box"] != 1 {
		t.Errorf("plastic style: got types %v, want one frame + one box", types)
	}
}

// Hidden pallet should publish exactly the (invisible) anchor frame —
// no child geometry — so the component keeps a presence in the scene.
func TestPalletGetVisuals_HiddenAnchor(t *testing.T) {
	p := newTestPallet(t)
	_, err := p.DoCommand(context.Background(), map[string]interface{}{
		"set_attributes": map[string]interface{}{"visible": false},
	})
	if err != nil {
		t.Fatalf("set_attributes visible=false: %v", err)
	}
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	entries, _ := resp["visuals"].([]map[string]interface{})
	if len(entries) != 1 {
		t.Fatalf("hidden pallet: got %d entries, want 1 (just the anchor frame)", len(entries))
	}
	if entries[0]["type"] != "frame" {
		t.Errorf("hidden pallet entry type = %v, want \"frame\"", entries[0]["type"])
	}
	if label, _ := entries[0]["label"].(string); label != "pallet/group" {
		t.Errorf("hidden anchor label = %q, want \"pallet/group\"", label)
	}
}

// Every component's get_visuals must emit exactly one parent anchor
// frame, and every non-frame entry must be parented to it. This is
// what lets the 3D viewer collapse / move a component as one unit.
func TestVisualsGroupedUnderParentFrame(t *testing.T) {
	cases := []struct {
		name      string
		resource  func(*testing.T) resource.Resource
		wantFrame string
	}{
		{"pallet", newTestPallet, "pallet/group"},
		{"pick-station", newTestPickStation, "pick-station/group"},
		{"safety-fence", newTestSafetyFence, "fence/group"},
		{"stack-light", newTestStackLight, "sl/group"},
		{"e-stop", newTestEStop, "estop/group"},
		{"workcell-bounds", newTestWorkcellBounds, "bounds/group"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.resource(t)
			resp, err := r.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
			if err != nil {
				t.Fatalf("get_visuals: %v", err)
			}
			entries, _ := resp["visuals"].([]map[string]interface{})
			frameCount := 0
			for _, e := range entries {
				ty, _ := e["type"].(string)
				label, _ := e["label"].(string)
				if ty == "frame" {
					frameCount++
					if label != tc.wantFrame {
						t.Errorf("anchor frame label = %q, want %q", label, tc.wantFrame)
					}
					if pf, _ := e["parent_frame"].(string); pf != "world" {
						t.Errorf("anchor frame parent = %q, want \"world\"", pf)
					}
					continue
				}
				// Every non-frame child must parent to the anchor.
				if pf, _ := e["parent_frame"].(string); pf != tc.wantFrame {
					t.Errorf("child %q parent_frame = %q, want %q", label, pf, tc.wantFrame)
				}
			}
			if frameCount != 1 {
				t.Errorf("got %d anchor frames, want exactly 1", frameCount)
			}
		})
	}
}

// Pick-station get_visuals must produce roller bed + rails + arrow.
func TestPickStationGetVisuals_ReturnsMultiPrimitive(t *testing.T) {
	p := newTestPickStation(t)
	resp, err := p.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	if err != nil {
		t.Fatalf("get_visuals: %v", err)
	}
	entries, ok := resp["visuals"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map, got %T", resp["visuals"])
	}
	labels := visualLabels(entries)
	mustContain(t, labels, "roller-00")
	mustContain(t, labels, "rail-0")
	mustContain(t, labels, "rail-1")
	mustContain(t, labels, "direction-arrow")
	mustContain(t, labels, "deck")
}

// helpers ---------------------------------------------------------------

func newTestPallet(t *testing.T) resource.Resource {
	t.Helper()
	conf := resource.Config{
		Name:  "pallet",
		API:   generic.API,
		Model: PalletModel,
		ConvertedAttributes: &PalletConfig{
			Label: "pallet",
		},
	}
	logger := logging.NewTestLogger(t)
	r, err := newPallet(context.Background(), nil, conf, logger)
	if err != nil {
		t.Fatalf("newPallet: %v", err)
	}
	return r
}

func newTestPickStation(t *testing.T) resource.Resource {
	t.Helper()
	conf := resource.Config{
		Name:  "pick-station",
		API:   generic.API,
		Model: PickStationModel,
		ConvertedAttributes: &PickStationConfig{
			Label:               "pick-station",
			LowestPointHeightMM: 700,
			BoxOriginOffsetMM:   &Vec3D{X: 200, Y: 200},
		},
	}
	logger := logging.NewTestLogger(t)
	r, err := newPickStation(context.Background(), nil, conf, logger)
	if err != nil {
		t.Fatalf("newPickStation: %v", err)
	}
	return r
}

// Phase E animation regression — roller_spin_period_s > 0 attaches a
// spin Animation to each roller; absent / zero leaves them static.
func TestPickStationGetVisuals_RollerSpinAnimation(t *testing.T) {
	p := newTestPickStation(t)
	// Default config: no roller_spin_period_s. Rollers must NOT have animation.
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	entries, _ := resp["visuals"].([]map[string]interface{})
	for _, e := range entries {
		label, _ := e["label"].(string)
		if !strings.Contains(label, "roller") {
			continue
		}
		if _, ok := e["animation"]; ok {
			t.Errorf("default roller %q unexpectedly carries animation %v", label, e["animation"])
		}
	}

	// Turn on spin.
	_, err := p.DoCommand(context.Background(), map[string]interface{}{
		"set_attributes": map[string]interface{}{"roller_spin_period_s": float64(2.0)},
	})
	if err != nil {
		t.Fatalf("set_attributes: %v", err)
	}
	resp, _ = p.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	entries, _ = resp["visuals"].([]map[string]interface{})
	foundSpin := 0
	for _, e := range entries {
		label, _ := e["label"].(string)
		if !strings.Contains(label, "roller") {
			continue
		}
		anim, ok := e["animation"].(map[string]interface{})
		if !ok {
			t.Errorf("roller %q missing animation after spin turned on", label)
			continue
		}
		if anim["mode"] != "spin" {
			t.Errorf("roller %q animation mode = %v, want spin", label, anim["mode"])
		}
		foundSpin++
	}
	// Default 400mm-long pick-station with adaptive roller-spacing
	// produces ~6 rollers; we accept ≥4 to leave room for future
	// tuning of the desired-spacing constant.
	if foundSpin < 4 {
		t.Errorf("expected ≥4 rollers with spin animation, got %d", foundSpin)
	}
}

// Animation dispatch requires SceneServiceBase.Scene to be non-nil
// after construction. Using ReconfigureWith leaves it nil; SetScene
// sets it. This test guards against future code accidentally
// reverting to ReconfigureWith — which would silently break every
// Animation (roller spin, stack-light flash, light-curtain pulse).
//
// We can't fully construct a workcellScene without a Viam dep graph
// here, so instead we exercise SetScene directly on a bare
// SceneServiceBase + verify Scene is populated. Mirrors the path
// newWorkcellScene takes.
func TestWorkcellScene_SetScenePopulatesSceneAndBaseVisuals(t *testing.T) {
	var base visuals.SceneServiceBase
	base.Logger = logging.NewTestLogger(t)
	base.DefaultTickHz = 30
	base.DefaultUUIDStrategy = "stable"
	base.DefaultParentFrame = "world"

	red := visuals.Color{R: 200, G: 30, B: 30}
	v := &visuals.Box{
		Label:  "test/box",
		Pose:   visuals.PoseAt(0, 0, 100, 0, 0, 1, 0),
		DimsMM: visuals.BoxDims{X: 100, Y: 100, Z: 100},
		Color:  &red,
	}
	if err := base.SetScene(
		visuals.SetSceneOpts{TickHz: 30, UUIDStrategy: "stable", ParentFrame: "world"},
		v,
	); err != nil {
		t.Fatalf("SetScene: %v", err)
	}
	if base.Scene == nil {
		t.Fatalf("after SetScene, SceneServiceBase.Scene is nil — animation dispatch is broken")
	}
	if base.Scene.Get("test/box") == nil {
		t.Errorf("Scene.Get('test/box') = nil; SetScene didn't install the visual")
	}
	_ = base.Close(context.Background())
}

// Tray-sized pallet (200 × 300 × 25) must render without stringer
// overlap. The library panics on Box construction if any DimsMM is
// ≤ 0; if the adaptive stringer width returns a value such that
// (length - stringerWidth) leaves no room for the inter-stringer
// step, the test will catch it. Beyond that, we verify slat count
// adapts down + stringer width adapts down.
func TestPalletGetVisuals_TraySized(t *testing.T) {
	conf := resource.Config{
		Name:  "tray",
		API:   generic.API,
		Model: PalletModel,
		ConvertedAttributes: &PalletConfig{
			Label:       "tray",
			WidthMM:     200,
			LengthMM:    300,
			ThicknessMM: 25,
		},
	}
	logger := logging.NewTestLogger(t)
	r, err := newPallet(context.Background(), nil, conf, logger)
	if err != nil {
		t.Fatalf("newPallet: %v", err)
	}
	resp, err := r.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	if err != nil {
		t.Fatalf("get_visuals: %v", err)
	}
	entries, ok := resp["visuals"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map, got %T", resp["visuals"])
	}

	// Every entry must have positive dims — sanity that adaptive
	// sizing didn't produce degenerate boxes.
	for i, e := range entries {
		label, _ := e["label"].(string)
		dims, ok := e["dims_mm"].(map[string]interface{})
		if !ok {
			continue // capsule / sphere entries — no dims_mm
		}
		x := asFloat(dims["x"])
		y := asFloat(dims["y"])
		z := asFloat(dims["z"])
		if x <= 0 || y <= 0 || z <= 0 {
			t.Errorf("entry %d %q has non-positive dims (%v, %v, %v)", i, label, x, y, z)
		}
	}

	// Slat count should adapt down for a 200mm pallet (175mm desired
	// slat width → round(200/175)=1, clamped to min=2). So we expect
	// ≤ 5 slats, not the GMA-pallet 7.
	slatCount := 0
	for _, e := range entries {
		l, _ := e["label"].(string)
		if strings.HasPrefix(l, "tray/slat-") {
			slatCount++
		}
	}
	if slatCount < 2 || slatCount > 5 {
		t.Errorf("tray slat count = %d, want 2..5 for a 200mm pallet", slatCount)
	}

	// Three stringers must not overlap on a 300mm tray. Adaptive
	// stringer width = min(90, length/(3*1.15)) = min(90, 87) = 87.
	// Each stringer is 87mm wide; centers spaced (length - width)/2
	// = (300-87)/2 = 106.5mm apart; edges touch with no overlap.
	// We assert the stringer Y dim is well under length/3 (= 100mm).
	for _, e := range entries {
		l, _ := e["label"].(string)
		if !strings.HasPrefix(l, "tray/stringer-") {
			continue
		}
		dims, _ := e["dims_mm"].(map[string]interface{})
		yWidth := asFloat(dims["y"])
		if yWidth >= 300.0/3 {
			t.Errorf("stringer %q width = %v, must be < length/3 = 100 to avoid overlap", l, yWidth)
		}
	}
}

// Even tinier pallet (150 × 150 × 15) — pathologically small to make
// sure no visual builder divides-by-zero or emits a negative dim.
func TestPalletGetVisuals_PathologicallySmall(t *testing.T) {
	conf := resource.Config{
		Name:  "micro-tray",
		API:   generic.API,
		Model: PalletModel,
		ConvertedAttributes: &PalletConfig{
			Label:       "micro-tray",
			WidthMM:     150,
			LengthMM:    150,
			ThicknessMM: 15,
		},
	}
	logger := logging.NewTestLogger(t)
	r, err := newPallet(context.Background(), nil, conf, logger)
	if err != nil {
		t.Fatalf("newPallet: %v", err)
	}
	resp, err := r.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	if err != nil {
		t.Fatalf("get_visuals: %v", err)
	}
	entries, _ := resp["visuals"].([]map[string]interface{})
	for i, e := range entries {
		label, _ := e["label"].(string)
		if dims, ok := e["dims_mm"].(map[string]interface{}); ok {
			x := asFloat(dims["x"])
			y := asFloat(dims["y"])
			z := asFloat(dims["z"])
			if x <= 0 || y <= 0 || z <= 0 {
				t.Errorf("entry %d %q has non-positive dims (%v, %v, %v)", i, label, x, y, z)
			}
		}
	}
}

// Small pick-station (180 × 200 × 30) must not produce overlapping
// rollers — adaptive roller count must scale down.
func TestPickStationGetVisuals_SmallSize(t *testing.T) {
	conf := resource.Config{
		Name:  "mini-conveyor",
		API:   generic.API,
		Model: PickStationModel,
		ConvertedAttributes: &PickStationConfig{
			Label:               "mini-conveyor",
			WidthMM:             180,
			LengthMM:            200,
			ThicknessMM:         30,
			LowestPointHeightMM: 500,
		},
	}
	logger := logging.NewTestLogger(t)
	r, err := newPickStation(context.Background(), nil, conf, logger)
	if err != nil {
		t.Fatalf("newPickStation: %v", err)
	}
	resp, err := r.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	if err != nil {
		t.Fatalf("get_visuals: %v", err)
	}
	entries, _ := resp["visuals"].([]map[string]interface{})

	rollerCount := 0
	for _, e := range entries {
		l, _ := e["label"].(string)
		if strings.HasPrefix(l, "mini-conveyor/roller-") {
			rollerCount++
		}
	}
	// length=200, sideRailWidth=min(30, 180/8)=22.5, usableLength≈155,
	// 60mm spacing → ceil(155/60) ≈ 3 rollers.
	if rollerCount < 3 || rollerCount > 6 {
		t.Errorf("small pick-station roller count = %d, want 3..6", rollerCount)
	}
}

// orphan reaping — when a component's get_visuals stops returning a
// label (style switch, visibility toggle), the corresponding scene
// entries must disappear, not pile up forever. We exercise this via
// the Scene + the reaping logic by simulating two refreshes.
func TestOrphanReaping_StyleSwitch(t *testing.T) {
	// Construct scene state by hand — we don't have a deps graph in
	// unit tests, but we can manually drive the Scene through the
	// same calls newWorkcellScene + refresh would.
	var base visuals.SceneServiceBase
	base.Logger = logging.NewTestLogger(t)
	base.DefaultParentFrame = "world"
	base.DefaultTickHz = 30
	base.DefaultUUIDStrategy = "stable"

	// First "refresh": stringer-style pallet (3 stringers + 7 slats + 3 bottoms).
	first := buildPalletVisuals(t, "stringer")
	if err := base.SetScene(visuals.SetSceneOpts{ParentFrame: "world"},
		visualsAsInterfaceSlice(first)...); err != nil {
		t.Fatalf("SetScene: %v", err)
	}
	stringerLabels := labelsContaining(base.Scene.Labels(), "/stringer-")
	if len(stringerLabels) != 3 {
		t.Fatalf("after stringer-style: got %d stringer labels, want 3", len(stringerLabels))
	}

	// Second "refresh": block style — stringers should reap, blocks add.
	second := buildPalletVisuals(t, "block")
	// Reaping mimics workcell-scene's refresh loop.
	presentLabels := make(map[string]bool)
	for _, v := range second {
		presentLabels[v.ToItem().Label] = true
	}
	for _, label := range base.Scene.Labels() {
		if presentLabels[label] {
			continue
		}
		owner, ok := ownerOfLabel(label)
		if !ok || owner != "test-pallet" {
			continue
		}
		base.Scene.Remove(label)
	}
	if _, err := base.Scene.AddOrUpdate(visualsAsInterfaceSlice(second)...); err != nil {
		t.Fatalf("AddOrUpdate: %v", err)
	}
	stringerLabels = labelsContaining(base.Scene.Labels(), "/stringer-")
	blockLabels := labelsContaining(base.Scene.Labels(), "/block-")
	if len(stringerLabels) != 0 {
		t.Errorf("after style switch to block: still have %d stringers (should be 0): %v",
			len(stringerLabels), stringerLabels)
	}
	if len(blockLabels) == 0 {
		t.Errorf("after style switch to block: got 0 block labels, want 9")
	}
}

// Stack-light "flash" state must attach a Flicker animation to that
// specific segment; "solid" segments stay static.
func TestStackLightGetVisuals_FlashAnimation(t *testing.T) {
	conf := resource.Config{
		Name:  "stack-light",
		API:   generic.API,
		Model: StackLightModel,
		ConvertedAttributes: &StackLightConfig{
			Label:  "stack-light",
			Colors: []string{"red", "yellow", "green"},
			States: []string{"solid", "flash", "off"},
		},
	}
	logger := logging.NewTestLogger(t)
	r, err := newStackLight(context.Background(), nil, conf, logger)
	if err != nil {
		t.Fatalf("newStackLight: %v", err)
	}
	resp, err := r.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	if err != nil {
		t.Fatalf("get_visuals: %v", err)
	}
	entries, _ := resp["visuals"].([]map[string]interface{})

	var redAnim, yellowAnim, greenAnim interface{}
	for _, e := range entries {
		label, _ := e["label"].(string)
		anim := e["animation"]
		switch {
		case strings.HasSuffix(label, "-red"):
			redAnim = anim
		case strings.HasSuffix(label, "-yellow"):
			yellowAnim = anim
		case strings.HasSuffix(label, "-green"):
			greenAnim = anim
		}
	}
	if redAnim != nil {
		t.Errorf("solid red segment unexpectedly animated: %v", redAnim)
	}
	if yellowAnim == nil {
		t.Errorf("flash yellow segment missing animation")
	} else if m, ok := yellowAnim.(map[string]interface{}); ok && m["mode"] != "flicker" {
		t.Errorf("flash yellow: animation mode = %v, want flicker", m["mode"])
	}
	if greenAnim != nil {
		t.Errorf("off green segment unexpectedly animated: %v", greenAnim)
	}
}

func visualLabels(entries []map[string]interface{}) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if l, ok := e["label"].(string); ok {
			out = append(out, l)
		}
	}
	return out
}

func mustContain(t *testing.T, labels []string, suffix string) {
	t.Helper()
	for _, l := range labels {
		if strings.HasSuffix(l, suffix) {
			return
		}
	}
	t.Errorf("expected a label ending in %q, got %v", suffix, labels)
}

func mustNotContain(t *testing.T, labels []string, suffix string) {
	t.Helper()
	for _, l := range labels {
		if strings.HasSuffix(l, suffix) {
			t.Errorf("expected NO label ending in %q, got one: %v", suffix, labels)
			return
		}
	}
}

// buildPalletVisuals builds the typed visuals for a test pallet
// using the given style. Returns []visuals.Visual ready to feed into
// scene.Add / scene.AddOrUpdate.
func buildPalletVisuals(t *testing.T, style string) []visuals.Visual {
	t.Helper()
	conf := resource.Config{
		Name:  "test-pallet",
		API:   generic.API,
		Model: PalletModel,
		ConvertedAttributes: &PalletConfig{
			Label: "test-pallet",
			Style: style,
		},
	}
	logger := logging.NewTestLogger(t)
	r, err := newPallet(context.Background(), nil, conf, logger)
	if err != nil {
		t.Fatalf("newPallet style=%q: %v", style, err)
	}
	resp, err := r.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	if err != nil {
		t.Fatalf("get_visuals: %v", err)
	}
	entries := coerceWireVisualsSlice(resp["visuals"])
	out := make([]visuals.Visual, 0, len(entries))
	for _, m := range entries {
		v, err := wireToVisual(m)
		if err != nil {
			t.Fatalf("wireToVisual: %v", err)
		}
		out = append(out, v)
	}
	return out
}

// Every attribute-bearing model must return a non-empty schema and
// every entry must have key + type + label fields. Guards against
// "I forgot to add get_schema to model X" regressions.
func TestEveryModelExposesGetSchema(t *testing.T) {
	cases := []struct {
		name     string
		resource func(*testing.T) resource.Resource
		wantKeys []string // sample of must-have keys
	}{
		{"pallet", newTestPallet, []string{"width_mm", "length_mm", "style", "color"}},
		{"pick-station", newTestPickStation, []string{"width_mm", "box_origin_offset_mm", "roller_spin_period_s"}},
		{"safety-fence", newTestSafetyFence, []string{"length_mm", "height_mm", "post_spacing_mm", "screen_opacity"}},
		{"light-curtain", newTestLightCurtain, []string{"span_mm", "beam_count", "state"}},
		{"e-stop", newTestEStop, []string{"height_mm", "color"}},
		{"stack-light", newTestStackLight, []string{"colors", "states", "segment_height_mm"}},
		{"tote-stack", newTestToteStack, []string{"box_dims_mm", "count", "stack_axis"}},
		{"robot-pedestal", newTestRobotPedestal, []string{"height_mm", "diameter_mm"}},
		{"hmi-cabinet", newTestHMICabinet, []string{"body_dims_mm", "body_color", "screen_color"}},
		{"floor-decal", newTestFloorDecal, []string{"length_mm", "width_mm", "stripe_pattern"}},
		{"workcell-bounds", newTestWorkcellBounds, []string{"length_mm", "width_mm", "height_mm", "edge_radius_mm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.resource(t)
			resp, err := r.DoCommand(context.Background(), map[string]interface{}{"get_schema": true})
			if err != nil {
				t.Fatalf("get_schema: %v", err)
			}
			entries, ok := resp["schema"].([]map[string]interface{})
			if !ok {
				t.Fatalf("expected []map, got %T", resp["schema"])
			}
			if len(entries) == 0 {
				t.Fatal("schema is empty")
			}
			seenKeys := make(map[string]bool, len(entries))
			for i, e := range entries {
				key, _ := e["key"].(string)
				typ, _ := e["type"].(string)
				label, _ := e["label"].(string)
				if key == "" || typ == "" || label == "" {
					t.Errorf("entry %d missing key/type/label: %v", i, e)
					continue
				}
				seenKeys[key] = true
			}
			for _, k := range tc.wantKeys {
				if !seenKeys[k] {
					t.Errorf("schema for %s missing key %q (got %v)", tc.name, k, sortedKeys(seenKeys))
				}
			}
		})
	}
}

// Conditional schema entries must carry a `when` block so the webapp
// can dim them when their dependency isn't satisfied. pallet's top-deck
// color depends on style; floor-decal's color depends on stripe_pattern.
func TestSchemaConditionalFields(t *testing.T) {
	cases := []struct {
		name, key, ctrlKey, notEquals string
		resource                      func(*testing.T) resource.Resource
	}{
		{"pallet", "color", "style", "plastic", newTestPallet},
		{"floor-decal", "color", "stripe_pattern", "hazard", newTestFloorDecal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.resource(t)
			resp, err := r.DoCommand(context.Background(), map[string]interface{}{"get_schema": true})
			if err != nil {
				t.Fatalf("get_schema: %v", err)
			}
			entries, _ := resp["schema"].([]map[string]interface{})
			var entry map[string]interface{}
			for _, e := range entries {
				if e["key"] == tc.key {
					entry = e
				}
			}
			if entry == nil {
				t.Fatalf("no %q entry in %s schema", tc.key, tc.name)
			}
			when, ok := entry["when"].(map[string]interface{})
			if !ok {
				t.Fatalf("%s %q entry missing `when` condition", tc.name, tc.key)
			}
			if when["key"] != tc.ctrlKey {
				t.Errorf("when.key = %v, want %q", when["key"], tc.ctrlKey)
			}
			if when["not_equals"] != tc.notEquals {
				t.Errorf("when.not_equals = %v, want %q", when["not_equals"], tc.notEquals)
			}
		})
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Per-model test-resource constructors used by TestEveryModelExposesGetSchema.

func newTestSafetyFence(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newSafetyFence(context.Background(), nil, resource.Config{
		Name: "fence", API: generic.API, Model: SafetyFenceModel,
		ConvertedAttributes: &SafetyFenceConfig{Label: "fence"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newSafetyFence: %v", err)
	}
	return r
}

func newTestLightCurtain(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newLightCurtain(context.Background(), nil, resource.Config{
		Name: "lc", API: generic.API, Model: LightCurtainModel,
		ConvertedAttributes: &LightCurtainConfig{Label: "lc"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newLightCurtain: %v", err)
	}
	return r
}

func newTestEStop(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newEStop(context.Background(), nil, resource.Config{
		Name: "estop", API: generic.API, Model: EStopModel,
		ConvertedAttributes: &EStopConfig{Label: "estop"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newEStop: %v", err)
	}
	return r
}

func newTestStackLight(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newStackLight(context.Background(), nil, resource.Config{
		Name: "sl", API: generic.API, Model: StackLightModel,
		ConvertedAttributes: &StackLightConfig{Label: "sl"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newStackLight: %v", err)
	}
	return r
}

func newTestToteStack(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newToteStack(context.Background(), nil, resource.Config{
		Name: "totes", API: generic.API, Model: ToteStackModel,
		ConvertedAttributes: &ToteStackConfig{Label: "totes"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newToteStack: %v", err)
	}
	return r
}

func newTestRobotPedestal(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newRobotPedestal(context.Background(), nil, resource.Config{
		Name: "ped", API: generic.API, Model: RobotPedestalModel,
		ConvertedAttributes: &RobotPedestalConfig{Label: "ped"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newRobotPedestal: %v", err)
	}
	return r
}

func newTestHMICabinet(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newHMICabinet(context.Background(), nil, resource.Config{
		Name: "hmi", API: generic.API, Model: HMICabinetModel,
		ConvertedAttributes: &HMICabinetConfig{Label: "hmi"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newHMICabinet: %v", err)
	}
	return r
}

func newTestFloorDecal(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newFloorDecal(context.Background(), nil, resource.Config{
		Name: "decal", API: generic.API, Model: FloorDecalModel,
		ConvertedAttributes: &FloorDecalConfig{Label: "decal"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newFloorDecal: %v", err)
	}
	return r
}

func newTestWorkcellBounds(t *testing.T) resource.Resource {
	t.Helper()
	r, err := newWorkcellBounds(context.Background(), nil, resource.Config{
		Name: "bounds", API: generic.API, Model: WorkcellBoundsModel,
		ConvertedAttributes: &WorkcellBoundsConfig{Label: "bounds"},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newWorkcellBounds: %v", err)
	}
	return r
}

// labelsContaining filters a label list by substring match.
func labelsContaining(labels []string, substr string) []string {
	out := []string{}
	for _, l := range labels {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

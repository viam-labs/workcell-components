package workcellcomponents

import (
	"context"
	"math"
	"testing"

	"github.com/golang/geo/r3"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/referenceframe"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

const eps = 1e-6

// boxHeightArg should accept either calling convention. The flat form
// matches the historical signature; the nested form matches the rest
// of the module's verb-arg pattern (set_dimensions, set_color, etc.)
// and is what a dryrun-natural caller (palletizer) sends.
func TestBoxHeightArg_NestedForm(t *testing.T) {
	cmd := map[string]interface{}{
		"get_vacuum_pose": map[string]interface{}{
			"box_height_mm": float64(60),
		},
	}
	got := boxHeightArg(cmd, cmd["get_vacuum_pose"])
	if math.Abs(got-60) > eps {
		t.Errorf("nested form: got %v, want 60", got)
	}
}

func TestBoxHeightArg_FlatForm(t *testing.T) {
	cmd := map[string]interface{}{
		"get_vacuum_pose": true,
		"box_height_mm":   float64(80),
	}
	got := boxHeightArg(cmd, cmd["get_vacuum_pose"])
	if math.Abs(got-80) > eps {
		t.Errorf("flat form: got %v, want 80", got)
	}
}

func TestBoxHeightArg_NestedTakesPrecedence(t *testing.T) {
	// If both are present, the nested arg wins — it's the more
	// specific source.
	cmd := map[string]interface{}{
		"get_vacuum_pose": map[string]interface{}{
			"box_height_mm": float64(60),
		},
		"box_height_mm": float64(999),
	}
	got := boxHeightArg(cmd, cmd["get_vacuum_pose"])
	if math.Abs(got-60) > eps {
		t.Errorf("nested+flat: got %v, want 60 (nested wins)", got)
	}
}

func TestBoxHeightArg_Missing(t *testing.T) {
	cmd := map[string]interface{}{"get_vacuum_pose": true}
	got := boxHeightArg(cmd, cmd["get_vacuum_pose"])
	if got != 0 {
		t.Errorf("missing arg: got %v, want 0", got)
	}
}

// Geometries should report a Box with the live dims, even after
// set_dimensions mutates them.
func TestPalletGeometries_DefaultDims(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{})
	geoms, err := p.Geometries(context.Background(), nil)
	if err != nil {
		t.Fatalf("Geometries: %v", err)
	}
	if len(geoms) != 1 {
		t.Fatalf("expected 1 geometry, got %d", len(geoms))
	}
	x, y, z := dimsOf(geoms[0])
	if math.Abs(x-DefaultPalletWidthMM) > eps ||
		math.Abs(y-DefaultPalletLengthMM) > eps ||
		math.Abs(z-DefaultPalletThicknessMM) > eps {
		t.Errorf("default dims: got (%v, %v, %v), want (%v, %v, %v)",
			x, y, z, DefaultPalletWidthMM, DefaultPalletLengthMM, DefaultPalletThicknessMM)
	}
}

func TestPalletGeometries_HonorsSetDimensions(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{})
	if _, err := p.DoCommand(context.Background(), map[string]interface{}{
		"set_dimensions": map[string]interface{}{
			"width_mm": 1000.0, "length_mm": 800.0, "thickness_mm": 120.0,
		},
	}); err != nil {
		t.Fatalf("set_dimensions: %v", err)
	}
	geoms, _ := p.Geometries(context.Background(), nil)
	x, y, z := dimsOf(geoms[0])
	if math.Abs(x-1000) > eps || math.Abs(y-800) > eps || math.Abs(z-120) > eps {
		t.Errorf("after set_dimensions: got (%v, %v, %v), want (1000, 800, 120)", x, y, z)
	}
}

func TestPickStationGeometries_DefaultDims(t *testing.T) {
	ps := newPickStationForTest(t, &PickStationConfig{})
	geoms, err := ps.Geometries(context.Background(), nil)
	if err != nil {
		t.Fatalf("Geometries: %v", err)
	}
	x, y, z := dimsOf(geoms[0])
	if math.Abs(x-DefaultPickStationWidthMM) > eps ||
		math.Abs(y-DefaultPickStationLengthMM) > eps ||
		math.Abs(z-DefaultPickStationThicknessMM) > eps {
		t.Errorf("default dims: got (%v, %v, %v), want (%v, %v, %v)",
			x, y, z, DefaultPickStationWidthMM, DefaultPickStationLengthMM, DefaultPickStationThicknessMM)
	}
}

// vacuumPose should respect box_height_mm — the pre-0.3.0 dryrun
// reported "z=220 regardless of box_height" because the handler read
// from the wrong arg-shape. With boxHeightArg fixed, the nested-arg
// form now flows through correctly.
func TestPickStationVacuumPose_HonorsBoxHeight(t *testing.T) {
	ps := newPickStationForTest(t, &PickStationConfig{
		BoxOriginOffsetMM: &Vec3D{X: 200, Y: 200},
	})
	for _, h := range []float64{60, 80, 100} {
		resp, err := ps.DoCommand(context.Background(), map[string]interface{}{
			"get_vacuum_pose": map[string]interface{}{
				"box_height_mm": h,
			},
		})
		if err != nil {
			t.Fatalf("h=%v: %v", h, err)
		}
		// Frame at zero pose; default thickness 40 → corner Z = +20.
		// vacuumPose adds boxHeightMM, so expected Z = 20 + h.
		gotZ := asFloat(resp["z"])
		wantZ := 20 + h
		if math.Abs(gotZ-wantZ) > eps {
			t.Errorf("h=%v: vacuum z got %v, want %v", h, gotZ, wantZ)
		}
	}
}

// --- helpers ---

// dimsOf pulls (x, y, z) from a Box geometry via its protobuf
// representation. The spatialmath.Box concrete type is unexported so
// the protobuf detour is the cleanest path to the dims.
func dimsOf(g spatialmath.Geometry) (x, y, z float64) {
	pb := g.ToProtobuf()
	box := pb.GetBox()
	if box == nil || box.DimsMm == nil {
		return 0, 0, 0
	}
	return box.DimsMm.X, box.DimsMm.Y, box.DimsMm.Z
}

func newPalletForTest(t *testing.T, cfg *PalletConfig) *pallet {
	t.Helper()
	r, err := newPallet(context.Background(), nil, resource.Config{
		Name:                "test-pallet",
		API:                 generic.API,
		Model:               PalletModel,
		ConvertedAttributes: cfg,
		Frame: &referenceframe.LinkConfig{
			Translation: r3.Vector{},
		},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newPallet: %v", err)
	}
	return r.(*pallet)
}

func newPickStationForTest(t *testing.T, cfg *PickStationConfig) *pickStation {
	t.Helper()
	r, err := newPickStation(context.Background(), nil, resource.Config{
		Name:                "test-pick-station",
		API:                 generic.API,
		Model:               PickStationModel,
		ConvertedAttributes: cfg,
		Frame: &referenceframe.LinkConfig{
			Translation: r3.Vector{},
		},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatalf("newPickStation: %v", err)
	}
	return r.(*pickStation)
}

// 0.4.0 — new external-surface verbs and attrs.

func TestPalletGetPalletHomePose_DefaultSafetyHeight(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{
		WidthMM: 600, LengthMM: 400, ThicknessMM: 100,
	})
	resp, err := p.DoCommand(context.Background(), map[string]interface{}{
		"get_pallet_home_pose": true,
	})
	if err != nil {
		t.Fatalf("DoCommand: %v", err)
	}
	// Frame at centroid (0, 0, 0); pallet home is at centroid +
	// (0, 0, thickness/2 + safety) = (0, 0, 250).
	if math.Abs(resp["x"].(float64)-0) > eps ||
		math.Abs(resp["y"].(float64)-0) > eps ||
		math.Abs(resp["z"].(float64)-250) > eps {
		t.Errorf("home pose: got (%v, %v, %v), want (0, 0, 250)",
			resp["x"], resp["y"], resp["z"])
	}
	// Gripper-down orientation.
	if math.Abs(resp["o_z"].(float64)-(-1)) > eps {
		t.Errorf("o_z: got %v, want -1", resp["o_z"])
	}
}

func TestPalletGetPalletHomePose_NestedSafetyHeight(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{
		WidthMM: 600, LengthMM: 400, ThicknessMM: 100,
	})
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{
		"get_pallet_home_pose": map[string]interface{}{"safety_height_mm": 500.0},
	})
	if math.Abs(resp["z"].(float64)-550) > eps {
		t.Errorf("z with safety=500: got %v, want 550 (50 + 500)", resp["z"])
	}
}

func TestPalletGetTopFaceCenter(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{
		WidthMM: 600, LengthMM: 400, ThicknessMM: 100,
	})
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{
		"get_top_face_center": true,
	})
	// Centroid at (0,0,0); top face center at (0, 0, +t/2 = 50).
	if math.Abs(resp["x"].(float64)-0) > eps ||
		math.Abs(resp["y"].(float64)-0) > eps ||
		math.Abs(resp["z"].(float64)-50) > eps {
		t.Errorf("top-face center: got (%v, %v, %v), want (0, 0, 50)",
			resp["x"], resp["y"], resp["z"])
	}
}

func TestPalletGetCornerPoses(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{
		WidthMM: 600, LengthMM: 400, ThicknessMM: 100,
	})
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{
		"get_corner_poses": true,
	})
	corners, ok := resp["corners"].([]map[string]interface{})
	if !ok {
		t.Fatalf("corners shape: got %T, want []map", resp["corners"])
	}
	if len(corners) != 4 {
		t.Fatalf("corner count: got %d, want 4", len(corners))
	}
	// Centroid at (0,0,0); top-face corners at centroid ± (w/2, l/2)
	// at Z = +t/2 = 50. CCW from BL.
	want := [4][2]float64{
		{-300, -200}, // BL
		{+300, -200}, // BR
		{+300, +200}, // TR
		{-300, +200}, // TL
	}
	for i, c := range corners {
		if math.Abs(c["x"].(float64)-want[i][0]) > eps ||
			math.Abs(c["y"].(float64)-want[i][1]) > eps {
			t.Errorf("corner %d: got (%v, %v), want %v", i, c["x"], c["y"], want[i])
		}
		if math.Abs(c["z"].(float64)-50) > eps {
			t.Errorf("corner %d Z: got %v, want 50", i, c["z"])
		}
	}
}

func TestPalletGetStatus(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{})
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{"get_status": true})
	if !resp["ok"].(bool) {
		t.Errorf("ok: got %v, want true", resp["ok"])
	}
	if !resp["visible"].(bool) {
		t.Errorf("default visible: got %v, want true", resp["visible"])
	}
	if resp["show_axes"].(bool) {
		t.Errorf("default show_axes: got %v, want false", resp["show_axes"])
	}
	if !resp["dims_valid"].(bool) {
		t.Errorf("dims_valid: got %v, want true", resp["dims_valid"])
	}
}

func TestPalletGetSummary(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{Label: "test"})
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{"get_summary": true})
	s := resp["summary"].(string)
	if !contains(s, "test") || !contains(s, "1219.2") {
		t.Errorf("summary missing label or dims: %q", s)
	}
}

func TestSetPersistHint(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{})
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{
		"set_dimensions": map[string]interface{}{"width_mm": 999.0},
	})
	if resp["persisted"].(bool) {
		t.Errorf("persisted: got true, want false")
	}
	if resp["hint"].(string) == "" {
		t.Error("hint should be non-empty")
	}
}

func TestSetAttributesVisualOptions(t *testing.T) {
	p := newPalletForTest(t, &PalletConfig{})
	resp, _ := p.DoCommand(context.Background(), map[string]interface{}{
		"set_attributes": map[string]interface{}{
			"show_axes": true,
			"visible":   false,
			"opacity":   0.5,
		},
	})
	if !resp["show_axes"].(bool) {
		t.Errorf("show_axes after set: got %v, want true", resp["show_axes"])
	}
	if resp["visible"].(bool) {
		t.Errorf("visible after set: got %v, want false", resp["visible"])
	}
	if math.Abs(resp["opacity"].(float64)-0.5) > eps {
		t.Errorf("opacity after set: got %v, want 0.5", resp["opacity"])
	}
}

func TestPickStationGetConveyorDirection_Default(t *testing.T) {
	ps := newPickStationForTest(t, &PickStationConfig{})
	resp, _ := ps.DoCommand(context.Background(), map[string]interface{}{
		"get_conveyor_direction": true,
	})
	if resp["y"].(float64) != 1 || resp["x"].(float64) != 0 || resp["z"].(float64) != 0 {
		t.Errorf("default conveyor direction: got %v, want (0, 1, 0)", resp)
	}
}

func TestPickStationSetConveyorDirection(t *testing.T) {
	ps := newPickStationForTest(t, &PickStationConfig{})
	_, _ = ps.DoCommand(context.Background(), map[string]interface{}{
		"set_attributes": map[string]interface{}{
			"conveyor_direction": map[string]interface{}{"x": 0.0, "y": -1.0, "z": 0.0},
		},
	})
	resp, _ := ps.DoCommand(context.Background(), map[string]interface{}{
		"get_conveyor_direction": true,
	})
	if resp["y"].(float64) != -1 {
		t.Errorf("after set: got %v, want y=-1", resp)
	}
}

func TestPickStationGetStatus(t *testing.T) {
	ps := newPickStationForTest(t, &PickStationConfig{})
	resp, _ := ps.DoCommand(context.Background(), map[string]interface{}{"get_status": true})
	if !resp["ok"].(bool) {
		t.Errorf("ok: got %v", resp["ok"])
	}
	if !resp["visible"].(bool) {
		t.Errorf("default visible: got %v", resp["visible"])
	}
}

// 0.5.0 — get_visual_pose for both components, used by workcell-scene
// to find the geometry centroid for the viz.Box transform.

func TestPalletGetVisualPose_IsCentroid(t *testing.T) {
	// Pallet's p.pose is the centroid (Viam convention), so
	// get_visual_pose == get_pose. Frame at zero pose, default
	// dims — visual pose should be (0, 0, 0).
	p := newPalletForTest(t, &PalletConfig{
		WidthMM: 600, LengthMM: 400, ThicknessMM: 100,
	})
	resp, err := p.DoCommand(context.Background(), map[string]interface{}{"get_visual_pose": true})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(resp["x"].(float64)) > eps ||
		math.Abs(resp["y"].(float64)) > eps ||
		math.Abs(resp["z"].(float64)) > eps {
		t.Errorf("pallet visual pose: got (%v, %v, %v), want (0, 0, 0)",
			resp["x"], resp["y"], resp["z"])
	}
}

func TestPickStationGetVisualPose_IsCentroidNotCorner(t *testing.T) {
	// Pick-station's p.pose is the bottom-left-top corner (set in
	// newPickStation for box-origin math convenience). get_visual_pose
	// must undo that offset to return the centroid. Frame at zero
	// pose; default dims 400×400×40. Corner pose at zero → corner is
	// at (-200, -200, +20) relative to centroid; centroid is at
	// (corner) + (+200, +200, -20) = (0, 0, 0).
	ps := newPickStationForTest(t, &PickStationConfig{})
	resp, err := ps.DoCommand(context.Background(), map[string]interface{}{"get_visual_pose": true})
	if err != nil {
		t.Fatal(err)
	}
	// Default dims 400×400×40. Frame at zero. corner_pose was set to
	// (0 + (-200, -200, +20), identity), so corner is at (-200,-200,+20).
	// Visual pose = corner + (+200, +200, -20) = (0, 0, 0).
	if math.Abs(resp["x"].(float64)) > eps ||
		math.Abs(resp["y"].(float64)) > eps ||
		math.Abs(resp["z"].(float64)) > eps {
		t.Errorf("pick-station visual pose: got (%v, %v, %v), want (0, 0, 0) (the centroid, not the corner)",
			resp["x"], resp["y"], resp["z"])
	}
	// Sanity check the corner pose IS at the corner (so we know
	// visual_pose != get_pose).
	cornerResp, _ := ps.DoCommand(context.Background(), map[string]interface{}{"get_pose": true})
	if math.Abs(cornerResp["x"].(float64)-(-200)) > eps {
		t.Errorf("pick-station corner pose: got x=%v, want -200 (corner offset)", cornerResp["x"])
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

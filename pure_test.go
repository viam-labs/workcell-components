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

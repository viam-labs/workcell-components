package workcellcomponents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The Viam 102 course fragment's pick-station attributes, minus the
// infeed_box_detect pairing (that needs a live sensor dependency). They set
// both a spin period and a box origin offset, which on 0.7.0-rc14 meant
// spinning rollers and a translucent next-box cube. The two defaults landed
// on separate branches; this pins them together.
const courseStationAttrs = `{
	"lowest_point_height_mm": 200.0,
	"box_origin_offset_mm": {"x": 200, "y": 900},
	"box_theta_deg": 0.0,
	"pick_home_z_offset_mm": 120.0,
	"length_mm": 1100,
	"roller_spin_period_s": 2.0
}`

func stationVisualsSummary(t *testing.T, p *pickStation) (rollers, animated int, target bool) {
	t.Helper()
	resp, err := p.DoCommand(context.Background(), map[string]interface{}{"get_visuals": true})
	if err != nil {
		t.Fatalf("get_visuals: %v", err)
	}
	vs, ok := resp["visuals"].([]map[string]interface{})
	if !ok {
		t.Fatalf("get_visuals: visuals is %T", resp["visuals"])
	}
	for _, v := range vs {
		label, _ := v["label"].(string)
		if strings.Contains(label, "/roller-") {
			rollers++
		}
		if v["animation"] != nil {
			animated++
		}
		if strings.HasSuffix(label, "/next-box-target") {
			target = true
		}
	}
	return rollers, animated, target
}

func TestCourseStationAttrs_StaticAndNoTargetUntilOptedIn(t *testing.T) {
	ctx := context.Background()
	var cfg PickStationConfig
	if err := json.Unmarshal([]byte(courseStationAttrs), &cfg); err != nil {
		t.Fatalf("parse course attrs: %v", err)
	}
	p := newPickStationForTest(t, &cfg)

	defaults, err := p.DoCommand(ctx, map[string]interface{}{"get_attributes": true})
	if err != nil {
		t.Fatalf("get_attributes: %v", err)
	}
	if defaults["show_next_box_target"] != false || defaults["animate_rollers"] != false {
		t.Errorf("get_attributes at course attrs: show_next_box_target=%v animate_rollers=%v, want false/false",
			defaults["show_next_box_target"], defaults["animate_rollers"])
	}

	rollers, animated, target := stationVisualsSummary(t, p)
	if rollers == 0 {
		t.Fatal("course attrs: roller bed not drawn")
	}
	if animated != 0 {
		t.Errorf("course attrs: %d animated entries, want 0 (roller_spin_period_s alone must not spin)", animated)
	}
	if target {
		t.Error("course attrs: next-box target rendered, want off by default")
	}

	if _, err := p.DoCommand(ctx, map[string]interface{}{
		"set_attributes": map[string]interface{}{
			"animate_rollers":      true,
			"show_next_box_target": true,
		},
	}); err != nil {
		t.Fatalf("set_attributes: %v", err)
	}
	_, animated, target = stationVisualsSummary(t, p)
	if animated == 0 {
		t.Error("animate_rollers=true: no animated entries")
	}
	if !target {
		t.Error("show_next_box_target=true: no next-box target")
	}

	attrs, err := p.DoCommand(ctx, map[string]interface{}{"get_attributes": true})
	if err != nil {
		t.Fatalf("get_attributes: %v", err)
	}
	if attrs["show_next_box_target"] != true || attrs["animate_rollers"] != true {
		t.Errorf("get_attributes: show_next_box_target=%v animate_rollers=%v, want true/true",
			attrs["show_next_box_target"], attrs["animate_rollers"])
	}

	if _, err := p.DoCommand(ctx, map[string]interface{}{
		"set_attributes": map[string]interface{}{"show_next_box_target": false},
	}); err != nil {
		t.Fatalf("set_attributes: %v", err)
	}
	if _, _, target = stationVisualsSummary(t, p); target {
		t.Error("show_next_box_target=false: target still rendered")
	}
}

func TestPickStationSchemaListsVisualToggles(t *testing.T) {
	keys := map[string]bool{}
	for _, e := range pickStationSchema() {
		keys[e.Key] = true
	}
	for _, want := range []string{"render_rollers", "animate_rollers", "show_next_box_target"} {
		if !keys[want] {
			t.Errorf("pick-station schema missing %q", want)
		}
	}
}

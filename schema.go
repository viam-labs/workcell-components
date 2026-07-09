package workcellcomponents

import (
	"encoding/json"
	"fmt"
)

// schemaEntry describes one attribute the operator can edit via the
// `get_schema` DoCommand verb. Webapps consume this list to
// auto-generate forms — one entry → one input. Order matters; the
// webapp renders entries top-to-bottom.
//
// The supported `type` values map to webapp input widgets:
//
//	"number"      → <input type=number step=Step>
//	"string"      → <input type=text>
//	"bool"        → <input type=checkbox>
//	"color"       → <input type=color> (RGB picker; webapp converts to {r,g,b})
//	"enum"        → <select> populated from Values
//	"enum_list"   → repeating <select> with add/remove; values from Values
//	"vec3"        → three <input type=number> (x/y/z), unit applies to all
//
// Group is an optional UI hint — "Identity", "Geometry", "Visual",
// "Behavior" — webapps can use it to render section dividers.
type schemaEntry struct {
	Key     string      `json:"key"`
	Type    string      `json:"type"`
	Label   string      `json:"label"`
	Group   string      `json:"group,omitempty"`
	Unit    string      `json:"unit,omitempty"`
	Min     *float64    `json:"min,omitempty"`
	Max     *float64    `json:"max,omitempty"`
	Step    *float64    `json:"step,omitempty"`
	Values  []string    `json:"values,omitempty"`
	Default interface{} `json:"default,omitempty"`
	Help    string      `json:"help,omitempty"`

	// When marks this attribute as conditionally relevant — meaningful
	// only while another attribute holds (or doesn't hold) a value.
	// e.g. a pallet's top-deck color is ignored when style="plastic".
	// Webapps dim the field + explain why when the condition is unmet;
	// the value is still stored so it's ready if the condition flips.
	When *schemaCondition `json:"when,omitempty"`
}

// schemaCondition is a dependency between two attributes: this field is
// relevant only when the field named Key equals (or does not equal) a
// given value. Exactly one of Equals / NotEquals is set.
type schemaCondition struct {
	Key       string      `json:"key"`
	Equals    interface{} `json:"equals,omitempty"`
	NotEquals interface{} `json:"not_equals,omitempty"`
}

// whenNot tags an entry as relevant only while attribute `key` does not
// equal `value`. Chainable: colorEntry(...).whenNot("style", "plastic").
func (e schemaEntry) whenNot(key string, value interface{}) schemaEntry {
	e.When = &schemaCondition{Key: key, NotEquals: value}
	return e
}

// whenIs tags an entry as relevant only while attribute `key` equals `value`.
func (e schemaEntry) whenIs(key string, value interface{}) schemaEntry {
	e.When = &schemaCondition{Key: key, Equals: value}
	return e
}

// withHelp attaches operator-facing help text to an entry.
func (e schemaEntry) withHelp(h string) schemaEntry {
	e.Help = h
	return e
}

// Common group labels — webapps use these to render section dividers.
const (
	schemaGroupIdentity = "Identity"
	schemaGroupGeometry = "Geometry"
	schemaGroupVisual   = "Visual"
	schemaGroupBehavior = "Behavior"
)

// numEntry builds a number-input schema entry with optional bounds.
// Pass 0 for any of min/max/step to omit that bound.
func numEntry(key, label, group, unit string, min, max, step float64) schemaEntry {
	e := schemaEntry{Key: key, Type: "number", Label: label, Group: group, Unit: unit}
	if min != 0 {
		v := min
		e.Min = &v
	}
	if max != 0 {
		v := max
		e.Max = &v
	}
	if step != 0 {
		v := step
		e.Step = &v
	}
	return e
}

func intEntry(key, label, group string, min, max float64) schemaEntry {
	e := numEntry(key, label, group, "", min, max, 1)
	return e
}

func colorEntry(key, label, group string) schemaEntry {
	return schemaEntry{Key: key, Type: "color", Label: label, Group: group}
}

func boolEntry(key, label, group string) schemaEntry {
	return schemaEntry{Key: key, Type: "bool", Label: label, Group: group}
}

func enumEntry(key, label, group string, values []string) schemaEntry {
	return schemaEntry{Key: key, Type: "enum", Label: label, Group: group, Values: values}
}

func enumListEntry(key, label, group string, values []string, help string) schemaEntry {
	return schemaEntry{Key: key, Type: "enum_list", Label: label, Group: group, Values: values, Help: help}
}

func stringEntry(key, label, group string) schemaEntry {
	return schemaEntry{Key: key, Type: "string", Label: label, Group: group}
}

func vec3Entry(key, label, group, unit string) schemaEntry {
	return schemaEntry{Key: key, Type: "vec3", Label: label, Group: group, Unit: unit}
}

// standardVisualSchema returns the attribute schema common to every
// visual component in this module: label, color, visibility, axes,
// opacity. Per-model schemas typically prepend Identity / Geometry
// entries and end with this block.
func standardVisualSchema() []schemaEntry {
	return []schemaEntry{
		colorEntry("color", "Color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// showAxesEntry is the standard show_axes toggle. Toggling it emits an
// X·Y·Z arrow triad at the component origin (see groupUnderFrame) — it
// does NOT touch the anchor frame's metadata, so it can't orphan the
// component's geometry.
func showAxesEntry() schemaEntry {
	return boolEntry("show_axes", "Show origin axes", schemaGroupVisual).
		withHelp("Red·green·blue X/Y/Z arrows at the component's origin — a placement & orientation aid.")
}

// schemaToResponse wraps a list of schema entries in the standard
// DoCommand response shape: {"schema": [...]}. Round-trips through
// encoding/json so each entry's JSON tags drive the wire format.
func schemaToResponse(entries []schemaEntry) (map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return nil, fmt.Errorf("marshal schema entry %q: %w", e.Key, err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("unmarshal schema entry %q: %w", e.Key, err)
		}
		out = append(out, m)
	}
	return map[string]interface{}{"schema": out}, nil
}

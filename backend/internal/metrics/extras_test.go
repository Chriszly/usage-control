package metrics

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func number(v float64) *float64 { return &v }

func TestCleanExtrasKeepsValidValues(t *testing.T) {
	extras := []Extra{{
		ID:     "pressure",
		Title:  "Pressure",
		Titles: map[string]string{"de": "Druck", "en-US": "Pressure"},
		Items: []ExtraItem{
			{ID: "cpu", Label: "CPU", Unit: UnitPercent, Value: number(3.5), History: true},
			{ID: "kernel", Label: "Kernel", Unit: UnitText, Text: "6.12"},
		},
	}}

	if got := CleanExtras(extras, 64); !reflect.DeepEqual(got, extras) {
		t.Errorf("CleanExtras() = %+v, want it unchanged", got)
	}
}

func TestCleanExtrasLeavesOutWhatCannotBeShown(t *testing.T) {
	extras := []Extra{
		{ID: "Bad ID", Title: "x", Items: []ExtraItem{{ID: "a", Unit: UnitNumber, Value: number(1)}}},
		{ID: "empty", Title: "No values"},
		{ID: "ok", Title: strings.Repeat("t", 200), Titles: map[string]string{"german": "x", "de": "y"}, Items: []ExtraItem{
			{ID: "a", Label: "A", Unit: UnitNumber, Value: number(1)},
			{ID: "a", Label: "Again", Unit: UnitNumber, Value: number(2)},
			{ID: "missing", Label: "No value", Unit: UnitWatts},
			{ID: "nan", Label: "NaN", Unit: UnitWatts, Value: number(math.NaN())},
			{ID: "future", Label: "New unit", Unit: "lux", Value: number(300), History: true},
			{ID: "text", Label: "Text", Unit: UnitText, Text: "hi", Value: number(1), History: true},
		}},
		{ID: "ok", Title: "Same id", Items: []ExtraItem{{ID: "a", Unit: UnitNumber, Value: number(1)}}},
	}

	got := CleanExtras(extras, 64)

	want := []Extra{{ID: "ok", Title: strings.Repeat("t", maxTextLength), Titles: map[string]string{"de": "y"}, Items: []ExtraItem{
		{ID: "a", Label: "A", Unit: UnitNumber, Value: number(1)},
		{ID: "future", Label: "New unit", Unit: UnitNumber, Value: number(300), History: true},
		{ID: "text", Label: "Text", Unit: UnitText, Text: "hi"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CleanExtras() = %+v, want %+v", got, want)
	}
}

func TestCleanExtrasKeepsAtMostMaxEntries(t *testing.T) {
	items := []ExtraItem{
		{ID: "a", Unit: UnitNumber, Value: number(1)},
		{ID: "b", Unit: UnitNumber, Value: number(2)},
		{ID: "c", Unit: UnitNumber, Value: number(3)},
	}
	extras := []Extra{{ID: "one", Items: items}, {ID: "two", Items: items}, {ID: "three", Items: items}}

	got := CleanExtras(extras, 2)

	if len(got) != 2 || len(got[0].Items) != 2 || len(got[1].Items) != 2 {
		t.Errorf("CleanExtras() = %+v, want 2 groups of 2 values", got)
	}
}

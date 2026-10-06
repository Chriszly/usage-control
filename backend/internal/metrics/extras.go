package metrics

import (
	"math"
	"regexp"
	"unicode/utf8"
)

// Extra is a group of values a device reports beyond the fixed fields of a
// Snapshot, such as the pressure on CPU, memory and disks. Each value says
// what it is and in which unit, so a hub can store and show values it does
// not know: a device running a newer version can report new metrics without
// the hub being updated first.
type Extra struct {
	// ID names the group, such as "pressure"; see validID.
	ID string `json:"id"`
	// Title is the group's title in English, and Titles the title in other
	// languages by language code, such as "de" or "en-US".
	Title  string            `json:"title"`
	Titles map[string]string `json:"titles,omitempty"`
	Items  []ExtraItem       `json:"items"`
}

// ExtraItem is one value of an Extra.
type ExtraItem struct {
	// ID names the value within its group; see validID.
	ID string `json:"id"`
	// Label is what the value is in English, and Labels the same in other
	// languages by language code.
	Label  string            `json:"label"`
	Labels map[string]string `json:"labels,omitempty"`
	Unit   Unit              `json:"unit"`
	// Value is set for every unit but UnitText, which sets Text instead.
	Value *float64 `json:"value,omitempty"`
	Text  string   `json:"text,omitempty"`
	// History asks a hub to keep the value's history and draw it as a chart.
	History bool `json:"history,omitempty"`
}

// Unit says how a value of an ExtraItem is shown.
type Unit string

// The units an ExtraItem can have. A unit a hub does not know yet is shown as
// a plain number.
const (
	UnitPercent        Unit = "percent"
	UnitCelsius        Unit = "celsius"
	UnitBytes          Unit = "bytes"
	UnitBytesPerSecond Unit = "bytesPerSecond"
	UnitWatts          Unit = "watts"
	UnitMilliseconds   Unit = "milliseconds"
	UnitPerSecond      Unit = "perSecond"
	UnitNumber         Unit = "number"
	UnitText           Unit = "text"
)

var knownUnits = map[Unit]bool{
	UnitPercent: true, UnitCelsius: true, UnitBytes: true, UnitBytesPerSecond: true, UnitWatts: true,
	UnitMilliseconds: true, UnitPerSecond: true, UnitNumber: true, UnitText: true,
}

const (
	// maxTextLength is the most characters a title, label or text keeps.
	maxTextLength = 80
	// maxTranslations is the most languages a title or label keeps.
	maxTranslations = 8
)

var (
	// validID matches the ids of groups and values: they are part of the
	// names values are stored under, so only short plain names.
	validID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	// validLanguage matches a language code such as "de" or "en-US".
	validLanguage = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)
)

// CleanExtras returns what can safely be stored and shown of the extras
// another device reported: at most maxEntries groups of at most maxEntries
// values each, every title, label and text cut to maxTextLength characters,
// and a unit the hub does not know turned into UnitNumber. Groups and values
// with an id that is invalid or already used, and values without a value for
// their unit, are left out.
func CleanExtras(extras []Extra, maxEntries int) []Extra {
	var clean []Extra
	groups := map[string]bool{}
	for _, group := range extras {
		if len(clean) == maxEntries {
			break
		}
		if !validID.MatchString(group.ID) || groups[group.ID] {
			continue
		}
		items := cleanItems(group.Items, maxEntries)
		if len(items) == 0 {
			continue
		}
		groups[group.ID] = true
		clean = append(clean, Extra{
			ID:     group.ID,
			Title:  cut(group.Title),
			Titles: cleanTranslations(group.Titles),
			Items:  items,
		})
	}
	return clean
}

func cleanItems(items []ExtraItem, maxEntries int) []ExtraItem {
	var clean []ExtraItem
	ids := map[string]bool{}
	for _, item := range items {
		if len(clean) == maxEntries {
			break
		}
		if !validID.MatchString(item.ID) || ids[item.ID] {
			continue
		}
		item.Label = cut(item.Label)
		item.Labels = cleanTranslations(item.Labels)
		if !knownUnits[item.Unit] {
			item.Unit = UnitNumber
		}
		if item.Unit == UnitText {
			item.Text, item.Value, item.History = cut(item.Text), nil, false
		} else {
			if item.Value == nil || math.IsNaN(*item.Value) || math.IsInf(*item.Value, 0) {
				continue
			}
			item.Text = ""
		}
		ids[item.ID] = true
		clean = append(clean, item)
	}
	return clean
}

func cleanTranslations(texts map[string]string) map[string]string {
	var clean map[string]string
	for language, text := range texts {
		if len(clean) == maxTranslations {
			break
		}
		if !validLanguage.MatchString(language) {
			continue
		}
		if clean == nil {
			clean = map[string]string{}
		}
		clean[language] = cut(text)
	}
	return clean
}

// cut returns text cut to maxTextLength characters, with invalid UTF-8
// replaced.
func cut(text string) string {
	if !utf8.ValidString(text) {
		text = string([]rune(text))
	}
	if utf8.RuneCountInString(text) <= maxTextLength {
		return text
	}
	return string([]rune(text)[:maxTextLength])
}

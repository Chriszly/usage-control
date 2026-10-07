package metrics

import (
	"cmp"
	"maps"
	"math"
	"regexp"
	"slices"
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
	// MaxTextLength is the most characters a title, label or text keeps, so
	// an add-on can make its texts fit.
	MaxTextLength = 80
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

// ValidID reports whether id can name a group of extras or a value in one.
func ValidID(id string) bool {
	return validID.MatchString(id)
}

// CleanExtras returns what can safely be stored and shown of the extras
// another device reported: at most maxEntries groups of at most maxEntries
// values each, every title, label and text cut to MaxTextLength characters,
// and a unit the hub does not know turned into UnitNumber. Groups and values
// with an id that is invalid or already used, and values without a value for
// their unit, are left out. Where there are more groups or values than kept,
// the ones that keep a history are kept first, then the others, each by id,
// in the order the device listed them: the same ones a hub keeps of the
// minutes it fetches, which have no order (see history.Keep).
func CleanExtras(extras []Extra, maxEntries int) []Extra {
	var clean []Extra
	groups := map[string]bool{}
	for _, group := range extras {
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
	return first(clean, maxEntries, func(group Extra) string { return group.ID }, func(group Extra) bool {
		return slices.ContainsFunc(group.Items, func(item ExtraItem) bool { return item.History })
	})
}

func cleanItems(items []ExtraItem, maxEntries int) []ExtraItem {
	var clean []ExtraItem
	ids := map[string]bool{}
	for _, item := range items {
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
	return first(clean, maxEntries, func(item ExtraItem) string { return item.ID }, func(item ExtraItem) bool { return item.History })
}

// first returns n of list, whose ids are unique, in the order of list: the
// ones that keep a history by id, then the others by id.
func first[T any](list []T, n int, id func(T) string, history func(T) bool) []T {
	if len(list) <= n {
		return list
	}
	ranked := slices.SortedFunc(slices.Values(list), func(a, b T) int {
		if history(a) != history(b) {
			if history(a) {
				return -1
			}
			return 1
		}
		return cmp.Compare(id(a), id(b))
	})
	kept := map[string]bool{}
	for _, entry := range ranked[:n] {
		kept[id(entry)] = true
	}
	return slices.DeleteFunc(list, func(entry T) bool { return !kept[id(entry)] })
}

// cleanTranslations keeps the valid translations, in the order of their
// language codes, so the same ones are kept on every read when there are too
// many.
func cleanTranslations(texts map[string]string) map[string]string {
	var clean map[string]string
	for _, language := range slices.Sorted(maps.Keys(texts)) {
		if len(clean) == maxTranslations {
			break
		}
		text := texts[language]
		if text == "" || !validLanguage.MatchString(language) {
			continue
		}
		if clean == nil {
			clean = map[string]string{}
		}
		clean[language] = cut(text)
	}
	return clean
}

// cut returns text cut to MaxTextLength characters, with invalid UTF-8
// replaced.
func cut(text string) string {
	if !utf8.ValidString(text) {
		text = string([]rune(text))
	}
	if utf8.RuneCountInString(text) <= MaxTextLength {
		return text
	}
	return string([]rune(text)[:MaxTextLength])
}

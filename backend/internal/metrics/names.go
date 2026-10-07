package metrics

import "fmt"

// NumberDuplicates numbers the names that occur more than once, in order:
// two "coretemp" become "coretemp 1" and "coretemp 2", so each name stands
// for one sensor or GPU in the history. Names that occur once are unchanged.
func NumberDuplicates(names []string) []string {
	count := make(map[string]int, len(names))
	for _, name := range names {
		count[name]++
	}
	seen := make(map[string]int, len(names))
	numbered := make([]string, len(names))
	for i, name := range names {
		numbered[i] = name
		if count[name] > 1 {
			seen[name]++
			numbered[i] = fmt.Sprintf("%s %d", name, seen[name])
		}
	}
	return numbered
}

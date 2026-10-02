package server

import (
	"cmp"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// sourceLanguage is the language the website is written in. The page is shown
// in it when the browser asks for none of the translated languages.
const sourceLanguage = "en"

// languageCookie holds the language picked in the page's language switcher.
const languageCookie = "lang"

// languages returns the languages the website is built in: every top-level
// folder of site with an index.html, such as "de" for site/de/index.html.
func languages(site fs.FS) []string {
	entries, err := fs.ReadDir(site, ".")
	if err != nil {
		return nil
	}
	var found []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := fs.Stat(site, entry.Name()+"/index.html"); err == nil {
			found = append(found, entry.Name())
		}
	}
	return found
}

// pickLanguage chooses which of the available languages to show the page in:
// the one picked in the language switcher, else the browser's preferred one
// from its Accept-Language header, else the source language.
func pickLanguage(r *http.Request, available []string) string {
	// Only names from available are returned, never the request's own text,
	// so the answer is always one of the website's folders.
	if cookie, err := r.Cookie(languageCookie); err == nil {
		if i := slices.Index(available, cookie.Value); i >= 0 {
			return available[i]
		}
	}
	for _, wanted := range acceptedLanguages(r.Header.Get("Accept-Language")) {
		if i := slices.Index(available, wanted); i >= 0 {
			return available[i]
		}
	}
	if slices.Contains(available, sourceLanguage) || len(available) == 0 {
		return sourceLanguage
	}
	return available[0]
}

// acceptedLanguages reads an Accept-Language header such as
// "de-CH, fr;q=0.8, en;q=0.5" and returns the primary language of each entry,
// most preferred first: ["de", "fr", "en"]. Entries with q=0 are left out.
func acceptedLanguages(header string) []string {
	type weighted struct {
		language string
		quality  float64
	}
	var accepted []weighted
	for entry := range strings.SplitSeq(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(entry), ";")
		language, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		if language == "" || language == "*" {
			continue
		}
		quality := 1.0
		if value, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				continue
			}
			quality = parsed
		}
		if quality > 0 {
			accepted = append(accepted, weighted{language, quality})
		}
	}
	// A stable sort keeps the header's order for entries with the same quality.
	slices.SortStableFunc(accepted, func(a, b weighted) int { return cmp.Compare(b.quality, a.quality) })

	languages := make([]string, len(accepted))
	for i, a := range accepted {
		languages[i] = a.language
	}
	return languages
}

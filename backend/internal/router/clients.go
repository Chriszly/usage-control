package router

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// client is one client of the router that is online, as the router names
// it and how it is connected.
type client struct {
	name string
	// wired is a client on a cable; band is the Wi-Fi band of one on Wi-Fi,
	// such as "5 GHz", where the router tells it.
	wired bool
	band  string
	// guest is a client on the guest network.
	guest bool
	// port is the router's LAN port of a wired client, 0 where the router
	// does not tell it.
	port int
}

// connectedExtra is the group of the clients online, each with how it is
// connected, by name.
func connectedExtra(clients []client) metrics.Extra {
	slices.SortStableFunc(clients, func(a, b client) int {
		return cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})
	extra := metrics.Extra{
		ID: "connected", Title: "Online now",
		Titles: map[string]string{"de": "Gerade online", "fr": "En ligne maintenant", "es": "En línea ahora"},
	}
	for i, c := range clients {
		text, texts := c.connection()
		extra.Items = append(extra.Items, metrics.ExtraItem{
			ID: fmt.Sprintf("client-%d", i+1), Label: c.name, Unit: metrics.UnitText, Text: text, Texts: texts,
		})
	}
	return extra
}

// connectionWords are the words of a connection in each language: a
// cable, Wi-Fi and the guest network.
var connectionWords = map[string][3]string{
	"en": {"Cable", "Wi-Fi", "Guest"},
	"de": {"Kabel", "WLAN", "Gast"},
	"fr": {"Câble", "Wi-Fi", "Invité"},
	"es": {"Cable", "Wi-Fi", "Invitado"},
}

// connection says in a few words how the client is connected, so its name
// keeps the room on the card: "Cable, LAN 2", "5 GHz" for Wi-Fi in that
// band, or "Guest, 2.4 GHz", in English and in the other languages.
func (c client) connection() (string, map[string]string) {
	in := func(language string) string {
		words := connectionWords[language]
		text := words[1]
		switch {
		case c.wired:
			text = words[0]
			if c.port > 0 {
				text += fmt.Sprintf(", LAN %d", c.port)
			}
		case c.band != "" && language != "en":
			text = strings.ReplaceAll(c.band, ".", ",")
		case c.band != "":
			text = c.band
		}
		if c.guest {
			text = words[2] + ", " + text
		}
		return text
	}
	texts := map[string]string{}
	for _, language := range []string{"de", "fr", "es"} {
		texts[language] = in(language)
	}
	return in("en"), texts
}

// clientName is the name a router gives a client, or else its address.
func clientName(names ...string) string {
	for _, name := range names {
		if name = strings.TrimSpace(strings.ToValidUTF8(name, "")); name != "" {
			return name
		}
	}
	return ""
}

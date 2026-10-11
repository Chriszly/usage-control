package router

import "testing"

func TestConnectedExtraNamesHowEachClientIsConnected(t *testing.T) {
	extra := connectedExtra([]client{
		{name: "phone", band: "2.4 GHz", guest: true},
		{name: "Desktop", wired: true, port: 3},
	})
	if len(extra.Items) != 2 {
		t.Fatalf("got %+v", extra.Items)
	}
	desktop, phone := extra.Items[0], extra.Items[1]
	if desktop.Label != "Desktop" || desktop.Text != "Cable, LAN 3" || desktop.Texts["fr"] != "Câble, LAN 3" {
		t.Errorf("desktop %+v", desktop)
	}
	if phone.Label != "phone" || phone.Text != "Guest, 2.4 GHz" || phone.Texts["de"] != "Gast, 2,4 GHz" {
		t.Errorf("phone %+v", phone)
	}
}

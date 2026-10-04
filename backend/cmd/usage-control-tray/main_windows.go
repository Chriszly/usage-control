//go:build windows

package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// serviceName is the service the installer sets up.
const serviceName = "UsageControl"

// checkEvery is how often the icon asks the service how it is.
const checkEvery = 5 * time.Second

//go:embed all:icons
var icons embed.FS

func main() {
	// One icon per logged-in user, however often it is started.
	instance, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(`Local\UsageControlTray`))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return
	}
	defer func() { _ = windows.CloseHandle(instance) }()
	languages, _ := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	t := &tray{
		text:   textsFor(languages),
		client: &http.Client{Timeout: 2 * time.Second},
		check:  make(chan struct{}, 1),
	}
	systray.Run(t.ready, nil)
}

type tray struct {
	text   texts
	client *http.Client
	// check asks for a check right away, after a menu action.
	check chan struct{}

	mu sync.Mutex
	// paused is set when the service was stopped with Pause.
	paused   bool
	hub      string
	settings settings
	state    state
	shown    string

	status, address, openHub, openPage, toggle, stop *systray.MenuItem
}

// ready builds the menu. windows/check-installer.ps1 clicks its entries by
// their number, which counts the entries and separators in this order.
func (t *tray) ready() {
	systray.SetTooltip("Usage Control")
	t.status = systray.AddMenuItem("Usage Control", "")
	t.status.Disable()
	t.address = systray.AddMenuItem(t.text.address, "")
	t.address.Disable()
	systray.AddSeparator()
	t.openHub = systray.AddMenuItem(t.text.noHub, "")
	t.openPage = systray.AddMenuItem(t.text.openPage, "")
	systray.AddSeparator()
	t.toggle = systray.AddMenuItem(t.text.pause, "")
	t.stop = systray.AddMenuItem(t.text.stopAndExit, "")
	t.refresh()
	go t.watch()
	go t.handleClicks()
}

// watch checks the service every few seconds, and after each menu action.
func (t *tray) watch() {
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-t.check:
		}
		t.refresh()
	}
}

func (t *tray) checkNow() {
	select {
	case t.check <- struct{}{}:
	default:
	}
}

// refresh reads the service's state and shows it.
func (t *tray) refresh() {
	current := readSettings()
	service := queryService()
	answers := false
	hub := ""
	if service == serviceRunning {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		link, err := askHub(ctx, t.client, current.port)
		cancel()
		answers, hub = err == nil, link
	}
	// Listing the adapters can take a moment, so it is done before taking mu.
	addr, found := localAddress()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused = stillPaused(service, t.paused)
	if hub != "" {
		t.hub = hub
	}
	t.settings = current
	t.state = stateOf(service, answers, t.paused)

	if icon := t.state.icon(); icon != t.shown {
		data, err := icons.ReadFile("icons/" + icon)
		if err != nil {
			showError("This tray program was built without its icons. Run go run ./cmd/tray-icons in backend/ before building it.")
			systray.Quit()
			return
		}
		systray.SetIcon(data)
		t.shown = icon
	}
	text := t.text.stateText(t.state)
	systray.SetTooltip("Usage Control: " + text)
	t.status.SetTitle("Usage Control: " + text)
	t.address.SetTitle(t.text.addressText(addr, found, t.settings.port))
	if t.hub != "" {
		t.openHub.SetTitle(t.text.openHub)
		t.openHub.Enable()
	} else {
		t.openHub.SetTitle(t.text.noHub)
		t.openHub.Disable()
	}
	if t.settings.website && t.state == stateRunning {
		t.openPage.Show()
	} else {
		t.openPage.Hide()
	}
	switch {
	case t.state == stateMissing:
		t.toggle.Hide()
		t.stop.SetTitle(t.text.stopAndExit)
	case t.state.running():
		t.toggle.SetTitle(t.text.pause)
		t.toggle.Show()
	case t.state == statePaused:
		t.toggle.SetTitle(t.text.resume)
		t.toggle.Show()
	default:
		t.toggle.SetTitle(t.text.start)
		t.toggle.Show()
	}
}

func (t *tray) handleClicks() {
	for {
		select {
		case <-t.openHub.ClickedCh:
			t.mu.Lock()
			link := t.hub
			t.mu.Unlock()
			if link != "" {
				openInBrowser(link)
			}
		case <-t.openPage.ClickedCh:
			t.mu.Lock()
			link := t.settings.pageURL()
			t.mu.Unlock()
			openInBrowser(link)
		case <-t.toggle.ClickedCh:
			t.mu.Lock()
			running := t.state.running()
			t.mu.Unlock()
			if running {
				if err := stopService(); err != nil {
					showError(fmt.Sprintf(t.text.stopFailed, err))
				} else {
					t.mu.Lock()
					t.paused = true
					t.mu.Unlock()
				}
			} else if err := startService(); err != nil {
				showError(fmt.Sprintf(t.text.startFailed, err))
			}
			t.checkNow()
		case <-t.stop.ClickedCh:
			if err := stopService(); err != nil {
				showError(fmt.Sprintf(t.text.stopFailed, err))
				t.checkNow()
				continue
			}
			systray.Quit()
			return
		}
	}
}

// readSettings reads the installer's remembered options.
func readSettings() settings {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Usage Control`, registry.QUERY_VALUE)
	if err != nil {
		return newSettings("", "")
	}
	defer func() { _ = key.Close() }()
	port, _, _ := key.GetStringValue("PORT")
	website, _, _ := key.GetStringValue("WEBSITE")
	return newSettings(port, website)
}

// openService opens the service with the given rights. The installer lets
// logged-in users start, stop and query it, so no administrator is needed.
func openService(access uint32) (windows.Handle, func(), error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, nil, err
	}
	service, err := windows.OpenService(manager, windows.StringToUTF16Ptr(serviceName), access)
	if err != nil {
		_ = windows.CloseServiceHandle(manager)
		return 0, nil, err
	}
	return service, func() {
		_ = windows.CloseServiceHandle(service)
		_ = windows.CloseServiceHandle(manager)
	}, nil
}

func queryService() serviceState {
	service, done, err := openService(windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return serviceMissing
	}
	if err != nil {
		return serviceStopped
	}
	defer done()
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(service, &status); err != nil {
		return serviceStopped
	}
	switch status.CurrentState {
	case windows.SERVICE_RUNNING:
		return serviceRunning
	case windows.SERVICE_START_PENDING, windows.SERVICE_CONTINUE_PENDING:
		return serviceStarting
	case windows.SERVICE_STOP_PENDING, windows.SERVICE_PAUSE_PENDING:
		return serviceStopping
	}
	return serviceStopped
}

func startService() error {
	service, done, err := openService(windows.SERVICE_START)
	if err != nil {
		return err
	}
	defer done()
	if err := windows.StartService(service, 0, nil); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return nil
}

func stopService() error {
	service, done, err := openService(windows.SERVICE_STOP)
	if err != nil {
		return err
	}
	defer done()
	var status windows.SERVICE_STATUS
	if err := windows.ControlService(service, windows.SERVICE_CONTROL_STOP, &status); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}
	return nil
}

func openInBrowser(link string) {
	_ = windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(link), nil, nil, windows.SW_SHOWNORMAL)
}

func showError(message string) {
	_, _ = windows.MessageBox(0, windows.StringToUTF16Ptr(message), windows.StringToUTF16Ptr("Usage Control"), windows.MB_OK|windows.MB_ICONWARNING)
}

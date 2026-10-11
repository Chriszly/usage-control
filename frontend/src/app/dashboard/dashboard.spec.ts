import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { HubConnection } from '../connection/connection';
import { DeviceService } from '../devices/devices';
import { I18n, LANGUAGE_STORAGE_KEY } from '../i18n/i18n';
import { Snapshot } from '../metrics/metrics';
import { Dashboard, REFRESH_INTERVAL_MS, coreColumns } from './dashboard';

const snapshot: Snapshot = {
  time: '2026-10-02T17:00:00Z',
  uptimeSeconds: 3 * 86400 + 4 * 3600,
  cpu: { usagePercent: 12.5, cores: 4 },
  memory: { totalBytes: 8 * 1024 ** 3, usedBytes: 2 * 1024 ** 3, usedPercent: 25 },
  temperatures: [{ sensor: 'cpu_thermal', celsius: 48.3 }],
  disks: [{ path: '/', totalBytes: 64 * 1024 ** 3, usedBytes: 16 * 1024 ** 3, usedPercent: 25 }],
  network: [
    {
      name: 'eth0',
      receivedBytes: 5 * 1024 ** 3,
      sentBytes: 1024 ** 3,
      receiveBytesPerSecond: 1.5 * 1024 ** 2,
      sendBytesPerSecond: 512,
    },
  ],
};

describe('Dashboard', () => {
  let fixture: ComponentFixture<Dashboard>;
  let http: HttpTestingController;

  beforeEach(() => {
    vi.useFakeTimers();
    // jsdom has no ResizeObserver, which the temperature card uses to fit its names.
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe = vi.fn();
        disconnect = vi.fn();
      },
    );
    TestBed.configureTestingModule({
      imports: [Dashboard],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(Dashboard);
  });

  afterEach(() => {
    http.verify();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  function respond(body: Snapshot | null): void {
    fixture.detectChanges();
    vi.advanceTimersByTime(0);
    const request = http.expectOne('/api/metrics');
    if (body) {
      request.flush(body);
    } else {
      request.flush('down', { status: 502, statusText: 'Bad Gateway' });
    }
    fixture.detectChanges();
  }

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  it('shows the values from the backend', () => {
    respond(snapshot);

    expect(text()).toContain('12.5 %');
    expect(text()).toContain('4 cores');
    expect(text()).toContain('2.0 GiB of 8.0 GiB');
    expect(text()).toContain('cpu_thermal');
    expect(text()).toContain('48.3 °C');
    expect(text()).toContain('3 d 4 h');
    expect(text()).toContain('16.0 GiB of 64.0 GiB');
    expect(text()).toContain('eth0');
    expect(text()).toContain('↓ 1.5 MiB/s');
    expect(text()).toContain('↑ 512 B/s');
  });

  it('shows per-core usage, clock, load average, swap and disk speed when reported', () => {
    respond({
      ...snapshot,
      cpu: {
        usagePercent: 30,
        cores: 2,
        coreUsagePercent: [20, 40],
        clockMHz: 1800,
        loadAverage: { one: 0.52, five: 0.4, fifteen: 0.31 },
      },
      memory: {
        ...snapshot.memory,
        swap: { totalBytes: 1024 ** 3, usedBytes: 256 * 1024 ** 2, usedPercent: 25 },
      },
      disks: [{ ...snapshot.disks[0], readBytesPerSecond: 2048, writeBytesPerSecond: 0 }],
    });

    const element = fixture.nativeElement as HTMLElement;
    const cores = element.querySelectorAll<HTMLElement>('.core');
    expect(cores.length).toBe(2);
    expect(cores[1].title).toBe('Core 2: 40 %');
    expect(element.querySelector('.cores')?.getAttribute('aria-label')).toBe(
      'Usage of each core: Core 1: 20 %, Core 2: 40 %',
    );
    expect(text().replace(/\s+/g, ' ')).toContain('2 cores · 1.8 GHz');
    expect(text()).toContain('Load 0.52 · 0.40 · 0.31');
    expect(text()).toContain('256.0 MiB of 1.0 GiB');
    expect(text()).toContain('Read 2.0 KiB/s');
    expect(text()).toContain('Write 0 B/s');
  });

  it('splits more than 8 cores evenly over several rows', () => {
    respond({
      ...snapshot,
      cpu: { usagePercent: 10, cores: 20, coreUsagePercent: Array<number>(20).fill(10) },
    });

    const cores = (fixture.nativeElement as HTMLElement).querySelector<HTMLElement>('.cores');
    expect(cores?.style.getPropertyValue('--core-columns')).toBe('7');
    expect([4, 8, 12, 16, 24, 32].map(coreColumns)).toEqual([4, 8, 6, 8, 8, 8]);
  });

  it("shows the device's version when it reports one", () => {
    respond({ ...snapshot, version: '0.1.0' });
    expect(text()).toContain('Version 0.1.0');
  });

  it("shows the time of the device's clock in its own time zone", () => {
    respond({ ...snapshot, timeZone: { name: 'CEST', offsetSeconds: 2 * 3600 } });
    expect(text().replace(/\s+/g, ' ')).toContain('7:00 PM');
    expect(text()).toContain('Friday, October 2, 2026 · CEST (UTC+2)');
  });

  it('names a zone by its offset when it has no short name, and leaves out UTC offsets', () => {
    respond({ ...snapshot, timeZone: { name: '-0330', offsetSeconds: -3.5 * 3600 } });
    expect(text().replace(/\s+/g, ' ')).toContain('1:30 PM');
    expect(text()).toContain('Friday, October 2, 2026 · UTC-3:30');

    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    http
      .expectOne('/api/metrics')
      .flush({ ...snapshot, timeZone: { name: 'UTC', offsetSeconds: 0 } });
    fixture.detectChanges();
    expect(text().replace(/\s+/g, ' ')).toContain('5:00 PM');
    expect(text()).toContain('Friday, October 2, 2026 · UTC');
  });

  it('leaves out the time when the device runs an older version', () => {
    respond(snapshot);
    expect(text()).not.toContain('Friday');
  });

  it('shows processes, I/O wait, memory breakdown, disk operations and network errors', () => {
    respond({
      ...snapshot,
      cpu: {
        ...snapshot.cpu,
        ioWaitPercent: 2.5,
        stealPercent: 0,
        processes: { total: 213, running: 2 },
      },
      memory: { ...snapshot.memory, availableBytes: 5 * 1024 ** 3, cachedBytes: 2 * 1024 ** 3 },
      disks: [
        {
          ...snapshot.disks[0],
          readBytesPerSecond: 0,
          writeBytesPerSecond: 0,
          operationsPerSecond: 12,
          busyPercent: 3,
          latencyMs: 0.8,
        },
      ],
      network: [{ ...snapshot.network[0], errors: 3, dropped: 120 }],
    });

    expect(text()).toContain('213 processes, 2 running');
    expect(text()).toContain('I/O wait 2.5 %');
    expect(text()).not.toContain('Steal');
    expect(text()).toContain('5.0 GiB available');
    expect(text()).toContain('2.0 GiB cache');
    expect(text()).toContain('12 operations/s');
    expect(text()).toContain('Busy 3 %');
    expect(text()).toContain('0.8 ms each');
    expect(text()).toContain('Errors 3 · Dropped 120');
  });

  it('shows fans, link speed and addresses, and battery power and health', () => {
    respond({
      ...snapshot,
      network: [{ ...snapshot.network[0], linkMbps: 1000, addresses: ['192.168.60.9'] }],
      fans: [{ name: 'pwmfan fan1', rpm: 3120 }],
      battery: { percent: 64, pluggedIn: false, watts: 8.25, healthPercent: 87.4 },
    });

    expect(text()).toContain('192.168.60.9 · 1 Gbit/s');
    expect(text()).toContain('Fans');
    expect(text()).toContain('3,120 rpm');
    expect(text()).toContain('Drawing 8.3 W');
    expect(text()).toContain('Health 87 %');
  });

  it('shows the extras as the device describes them, in the language of the page', () => {
    respond({
      ...snapshot,
      extras: [
        {
          id: 'pressure',
          title: 'Pressure',
          titles: { de: 'Druck' },
          items: [
            { id: 'cpu', label: 'CPU waiting', unit: 'percent', value: 3.25, history: true },
            { id: 'power', label: 'Package', unit: 'watts', value: 12.5 },
            { id: 'kernel', label: 'Kernel', unit: 'text', text: '6.12.1' },
            { id: 'laptop', label: 'Laptop', unit: 'text', text: 'Cable', texts: { de: 'Kabel' } },
          ],
        },
      ],
    });

    expect(text()).toContain('Pressure');
    expect(text()).toContain('CPU waiting3.3 %');
    expect(text()).toContain('Package12.5 W');
    expect(text()).toContain('Kernel6.12.1');

    TestBed.inject(I18n).use('de');
    fixture.detectChanges();
    expect(text()).toContain('Druck');
    expect(text()).toContain('12,5 W');
    expect(text()).toContain('LaptopKabel');
    localStorage.removeItem(LANGUAGE_STORAGE_KEY);
  });

  it('leaves out what the device does not report', () => {
    respond(snapshot);

    const element = fixture.nativeElement as HTMLElement;
    expect(element.querySelector('.cores')).toBeNull();
    expect(text()).not.toContain('GHz');
    expect(text()).not.toContain('Load');
    expect(text()).not.toContain('Swap');
    expect(text()).not.toContain('Read');
    expect(text()).not.toContain('Power and clock');
    expect(text()).not.toContain('Version');
    expect(text()).not.toContain('Battery');
    expect(text()).not.toContain('processes');
    expect(text()).not.toContain('I/O wait');
    expect(text()).not.toContain('available');
    expect(text()).not.toContain('Errors');
    expect(text()).not.toContain('Fans');
    expect(text()).not.toContain('Mbit/s');
  });

  it('shows the battery charge and whether the machine is plugged in', () => {
    respond({ ...snapshot, battery: { percent: 64, pluggedIn: false } });
    expect(text()).toContain('Battery');
    expect(text()).toContain('64 %');
    expect(text()).toContain('On battery');

    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    http
      .expectOne('/api/metrics')
      .flush({ ...snapshot, battery: { percent: 65, pluggedIn: true } });
    fixture.detectChanges();
    expect(text()).toContain('Plugged in');
  });

  it("shows a Raspberry Pi's power and throttling state", () => {
    respond({ ...snapshot, throttling: { now: [], sinceBoot: [] } });
    expect(text()).toContain('Power and clock');
    expect(text()).toContain('No undervoltage or throttling since start');

    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    http.expectOne('/api/metrics').flush({
      ...snapshot,
      throttling: {
        now: ['undervoltage'],
        sinceBoot: ['undervoltage', 'throttled'],
      },
    });
    fixture.detectChanges();
    expect(text()).toContain('Warning');
    expect(text()).toContain('Now: Undervoltage');
    expect(text()).toContain('Earlier since start: Throttled');
  });

  it('says "core" for a single core', () => {
    respond({ ...snapshot, cpu: { usagePercent: 3, cores: 1 } });

    expect(text()).toContain('1 core');
    expect(text()).not.toContain('1 cores');
  });

  it('says when there is no disk or network card to show', () => {
    respond({ ...snapshot, disks: [], network: [] });

    expect(text()).toContain('No disk configured');
    expect(text()).toContain('No network card found');
  });

  it('lists busy interfaces first and hides ones without traffic', () => {
    const idle = {
      receivedBytes: 0,
      sentBytes: 0,
      receiveBytesPerSecond: 0,
      sendBytesPerSecond: 0,
    };
    respond({
      ...snapshot,
      network: [
        { ...idle, name: 'Bluetooth-Netzwerkverbindung' },
        { ...idle, name: 'WLAN', receivedBytes: 2048, sentBytes: 1024 },
        { ...snapshot.network[0], name: 'Ethernet' },
        { ...idle, name: 'LAN-Verbindung* 10' },
      ],
    });

    const names = Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('.interface .label'),
      (label) => label.getAttribute('title'),
    );
    expect(names).toEqual(['Ethernet', 'WLAN']);
    expect(text()).toContain('2 interfaces without traffic hidden');
  });

  it('says when no temperature sensor is available', () => {
    respond({ ...snapshot, temperatures: [] });

    expect(text()).toContain('Not available on this machine');
  });

  it('shows each GPU with its usage, memory and temperature', () => {
    respond({
      ...snapshot,
      gpus: [
        {
          name: 'AMD Radeon RX 7800 XT',
          usagePercent: 63.2,
          memoryTotalBytes: 16 * 1024 ** 3,
          memoryUsedBytes: 4 * 1024 ** 3,
          celsius: 57,
        },
        { name: 'VideoCore GPU', usagePercent: 4 },
      ],
    });

    expect(text()).toContain('GPU');
    expect(text()).toContain('AMD Radeon RX 7800 XT');
    expect(text()).toContain('63.2 %');
    expect(text()).toContain('4.0 GiB of 16.0 GiB memory');
    expect(text()).toContain('57 °C');
    expect(text()).toContain('VideoCore GPU');
  });

  it('leaves out the GPU card when there is no GPU, or the device runs an older version', () => {
    respond({ ...snapshot, gpus: [] });
    const titles = () =>
      [...(fixture.nativeElement as HTMLElement).querySelectorAll('h2')].map((h) => h.textContent);

    expect(titles()).not.toContain('GPU');

    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    http.expectOne('/api/metrics').flush(snapshot);
    fixture.detectChanges();

    expect(titles()).not.toContain('GPU');
  });

  it('switches language in place, with numbers in that language', () => {
    respond(snapshot);

    TestBed.inject(I18n).use('de');
    fixture.detectChanges();

    expect(text()).toContain('Arbeitsspeicher');
    expect(text()).toContain('12,5 %');
    expect(text()).toContain('4 Kerne');
    expect(text()).toContain('2,0 GiB von 8,0 GiB');
    localStorage.removeItem(LANGUAGE_STORAGE_KEY);
  });

  it('keeps the last values and warns when the backend is unreachable', () => {
    respond(snapshot);
    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    respond(null);

    expect(text()).toContain('cannot be reached');
    expect(text()).toContain('12.5 %');
  });

  it('leaves the warning to the banner and fades the cards while the hub does not answer', () => {
    respond(snapshot);
    TestBed.inject(HubConnection).failed();
    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    respond(null);

    expect(text()).not.toContain('cannot be reached');
    expect(text()).toContain('12.5 %');
    const cards = (fixture.nativeElement as HTMLElement).querySelector('.cards');
    expect(cards?.classList).toContain('stale');

    TestBed.inject(HubConnection).answered();
    fixture.detectChanges();
    expect(cards?.classList).not.toContain('stale');
  });

  it('skips a refresh while the previous reading is still on its way', () => {
    respond(snapshot);
    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    const slow = http.expectOne('/api/metrics');

    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    http.expectNone('/api/metrics');

    slow.flush(snapshot);
    vi.advanceTimersByTime(REFRESH_INTERVAL_MS);
    http.expectOne('/api/metrics').flush(snapshot);
  });

  it('stops refreshing while the page is hidden and refreshes at once when it is shown again', () => {
    let visibilityState: DocumentVisibilityState = 'visible';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibilityState);
    respond(snapshot);

    visibilityState = 'hidden';
    document.dispatchEvent(new Event('visibilitychange'));
    vi.advanceTimersByTime(10 * REFRESH_INTERVAL_MS);
    http.expectNone('/api/metrics');

    visibilityState = 'visible';
    document.dispatchEvent(new Event('visibilitychange'));
    respond(snapshot);
    expect(text()).toContain('12.5 %');
  });

  it('shows the picked device and says when it does not answer', () => {
    respond(snapshot);
    const devices = TestBed.inject(DeviceService);
    devices.devices.set([
      { id: 'local', name: '' },
      { id: 'living-room-pi', name: 'Living room Pi' },
    ]);
    devices.selectedId.set('living-room-pi');
    fixture.detectChanges();
    vi.advanceTimersByTime(0);

    http
      .expectOne('/api/metrics?device=living-room-pi')
      .flush('down', { status: 503, statusText: 'Service Unavailable' });
    http
      .expectOne('/api/availability?device=living-room-pi')
      .flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();

    expect(text()).toContain('Living room Pi has not answered recently');
    expect(text()).not.toContain('12.5 %');
  });

  it('asks whether a router that does not answer is on and set up right', () => {
    respond(snapshot);
    const devices = TestBed.inject(DeviceService);
    devices.devices.set([
      { id: 'local', name: '' },
      { id: 'router', name: 'Router', kind: 'server', address: '192.168.1.1', router: true },
    ]);
    devices.selectedId.set('router');
    fixture.detectChanges();
    vi.advanceTimersByTime(0);

    http
      .expectOne('/api/metrics?device=router')
      .flush('down', { status: 503, statusText: 'Service Unavailable' });
    http
      .expectOne('/api/availability?device=router')
      .flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();

    expect(text()).toContain('Router has not answered recently. Is it on, and are its address');
    expect(text()).not.toContain('usage-control running');
  });

  it('points to HUB_DEVICES for a device at an address of the hub, without its availability', () => {
    vi.setSystemTime(new Date('2026-10-02T12:00:00Z'));
    respond(snapshot);
    const devices = TestBed.inject(DeviceService);
    devices.devices.set([
      { id: 'local', name: '' },
      { id: 'vm', name: 'VM', kind: 'pc', unreachable: true, refused: true },
    ]);
    devices.selectedId.set('vm');
    fixture.detectChanges();
    vi.advanceTimersByTime(0);

    http
      .expectOne('/api/metrics?device=vm')
      .flush('down', { status: 503, statusText: 'Service Unavailable' });
    // The time it was refused is not counted, so it would seem to have always answered.
    http.expectOne('/api/availability?device=vm').flush({
      kind: 'pc',
      since: '2026-10-01T12:00:00Z',
      countedSince: '2026-10-01T12:00:00Z',
      offlineSeconds: 0,
      outages: 0,
    });
    fixture.detectChanges();

    expect(text()).toContain('VM is at an address of the hub itself');
    expect(text()).toContain('HUB_DEVICES');
    expect(text()).not.toContain('has not answered recently');
    expect(text()).not.toContain('switched off');
    expect(text()).not.toContain('100 %');
    expect(text()).not.toContain('Switched on all the time');
  });

  it('shows only what a router tells, with how it is read and that nothing leaves the network', () => {
    vi.setSystemTime(new Date('2026-10-02T12:00:00Z'));
    respond(snapshot);
    const devices = TestBed.inject(DeviceService);
    devices.devices.set([
      { id: 'local', name: '' },
      { id: 'router', name: 'Router', kind: 'server', address: '192.168.1.1', router: true },
    ]);
    devices.selectedId.set('router');
    fixture.detectChanges();
    vi.advanceTimersByTime(0);

    // A router read over UPnP: the internet traffic only.
    http.expectOne('/api/metrics?device=router').flush({
      time: '2026-10-02T12:00:00Z',
      uptimeSeconds: 0,
      cpu: { usagePercent: 0, cores: 0 },
      memory: { totalBytes: 0, usedBytes: 0, usedPercent: 0 },
      temperatures: [],
      disks: [],
      network: [
        {
          name: 'WAN',
          receivedBytes: 1024 ** 3,
          sentBytes: 1024 ** 2,
          receiveBytesPerSecond: 2 * 1024 ** 2,
          sendBytesPerSecond: 1024,
        },
      ],
    } satisfies Snapshot);
    http.expectOne('/api/availability?device=router').flush({
      kind: 'server',
      since: '2026-10-01T12:00:00Z',
      countedSince: '2026-10-01T12:00:00Z',
      offlineSeconds: 0,
      outages: 0,
    });
    fixture.detectChanges();

    const titles = Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('.card h2'),
      (h) => h.textContent?.trim(),
    );
    expect(titles).toEqual(['Network', 'Availability']);
    expect(text()).toContain('WAN');
  });

  it('shows how long another device was offline since it was added', () => {
    vi.setSystemTime(new Date('2026-10-02T12:00:00Z'));
    respond(snapshot);
    const devices = TestBed.inject(DeviceService);
    devices.devices.set([
      { id: 'local', name: '' },
      { id: 'living-room-pi', name: 'Living room Pi' },
    ]);
    devices.selectedId.set('living-room-pi');
    fixture.detectChanges();
    vi.advanceTimersByTime(0);

    http
      .expectOne('/api/metrics?device=living-room-pi')
      .flush('down', { status: 503, statusText: 'Service Unavailable' });
    http.expectOne('/api/availability?device=living-room-pi').flush({
      kind: 'server',
      since: '2026-10-01T12:00:00Z',
      countedSince: '2026-10-01T12:00:00Z',
      offlineSeconds: 864,
      outages: 1,
      lastOutage: { start: '2026-10-02T11:00:00Z', end: '2026-10-02T11:14:24Z' },
    });
    fixture.detectChanges();

    expect(text()).toContain('Availability');
    expect(text()).toContain('99 %');
    expect(text()).toContain('Offline for 14 min since');
    expect(text()).toContain('1 outage, the last on');
    expect(text()).toContain('for 14 min');
  });

  it('shows the time a PC was off as not in use, not as outages', () => {
    vi.setSystemTime(new Date('2026-10-02T12:00:00Z'));
    respond(snapshot);
    const devices = TestBed.inject(DeviceService);
    devices.devices.set([
      { id: 'local', name: '' },
      { id: 'laptop', name: 'Laptop', kind: 'pc', unreachable: true },
    ]);
    devices.selectedId.set('laptop');
    fixture.detectChanges();
    vi.advanceTimersByTime(0);

    http
      .expectOne('/api/metrics?device=laptop')
      .flush('down', { status: 503, statusText: 'Service Unavailable' });
    http.expectOne('/api/availability?device=laptop').flush({
      kind: 'pc',
      // Removed with its history kept for a while and added again: its off times are counted
      // since the first, its share over the time it was watched.
      since: '2026-09-20T08:00:00Z',
      countedSince: '2026-10-01T12:00:00Z',
      offlineSeconds: 43200,
      outages: 2,
      lastOutage: { start: '2026-10-02T11:00:00Z', end: '2026-10-02T12:00:00Z' },
    });
    fixture.detectChanges();

    expect(text()).toContain('Laptop is switched off or asleep, so it is not in use.');
    expect(fixture.nativeElement.querySelector('.warning')).toBeNull();
    expect(text()).toContain('Usage');
    expect(text()).not.toContain('Availability');
    expect(text()).toContain('50 %');
    expect(text()).toContain('Not in use for 12 h');
    expect(text()).toContain('Off 2 times, the last on');
    expect(text()).not.toContain('outage');
  });

  it('leaves out the availability for this device', () => {
    respond(snapshot);

    expect(text()).not.toContain('Availability');
  });
});

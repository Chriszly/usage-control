import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { DeviceService } from '../devices/devices';
import { I18n, LANGUAGE_STORAGE_KEY } from '../i18n/i18n';
import { Snapshot } from '../metrics/metrics';
import { Dashboard, REFRESH_INTERVAL_MS } from './dashboard';

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
    fixture.detectChanges();

    expect(text()).toContain('Living room Pi has not answered recently');
    expect(text()).not.toContain('12.5 %');
  });
});

import { HttpBackend, provideHttpClient } from '@angular/common/http';
import { TestBed } from '@angular/core/testing';
import { firstValueFrom } from 'rxjs';

import { DeviceService, LOCAL_DEVICE, problemMessage } from '../app/devices/devices';
import { I18n } from '../app/i18n/i18n';
import { MetricsService } from '../app/metrics/metrics';
import { DEMO_RETENTION_DAYS, DemoBackend } from './demo-backend';
import { demoVersion } from './demo-build';
import { FLEET } from './fleet';

describe('DemoBackend', () => {
  let metrics: MetricsService;
  let devices: DeviceService;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), { provide: HttpBackend, useClass: DemoBackend }],
    });
    metrics = TestBed.inject(MetricsService);
    devices = TestBed.inject(DeviceService);
  });

  it('lists Windows and Linux devices, one of them asleep', async () => {
    const list = await firstValueFrom(devices.load());
    expect(list.devices[0]).toEqual(LOCAL_DEVICE);
    expect(list.devices.filter((d) => d.unreachable).map((d) => d.name)).toEqual(['Linux laptop']);
    expect(FLEET.some((m) => m.os === 'windows')).toBe(true);
  });

  it('answers with values each device reports', async () => {
    for (const machine of FLEET.filter((m) => !m.online)) {
      const s = await firstValueFrom(metrics.current(machine.device.id));
      expect(s.cpu.cores).toBe(machine.cores);
      expect(s.cpu.usagePercent).toBeGreaterThanOrEqual(0);
      expect(s.cpu.usagePercent).toBeLessThanOrEqual(100);
      expect(s.disks.map((d) => d.path)).toEqual(machine.disks.map((d) => d.path));
      // Windows and routers have no load average; Windows no temperature sensors the backend reads.
      expect(s.cpu.loadAverage === undefined).toBe(machine.os === 'windows' || !!machine.router);
      expect(s.temperatures.length === 0).toBe(machine.os === 'windows');
    }
  });

  it('shows a router with what an ASUS router tells and no disks', async () => {
    const router = FLEET.find((m) => m.router)!;
    expect(router.device.router).toBe(true);
    const s = await firstValueFrom(metrics.current(router.device.id));
    expect(s.disks).toEqual([]);
    expect(s.version).toBeUndefined();
    expect(s.network.map((n) => n.name)).toEqual(['WAN', 'LAN', 'Wi-Fi 2.4 GHz', 'Wi-Fi 5 GHz']);
    expect(s.extras?.[0].items[0].value).toBeGreaterThan(0);
  });

  it('reports the version the demo was built from, like the backend', async () => {
    // A test build sets neither a release nor a commit.
    expect((await firstValueFrom(metrics.current(LOCAL_DEVICE.id))).version).toBe('dev');
    expect(demoVersion({ release: '1.2.0', commit: 'a690bc7' })).toBe('1.2.0');
    expect(demoVersion({ commit: 'a690bc7' })).toBe('a690bc7');
  });

  it('gives every value of the add-ons a number, the processes busiest first', async () => {
    for (const machine of FLEET.filter((m) => !m.online)) {
      const s = await firstValueFrom(metrics.current(machine.device.id));
      for (const group of s.extras ?? []) {
        const values = group.items.filter((i) => i.unit !== 'text').map((i) => i.value);
        expect(
          values.every((v) => typeof v === 'number'),
          `${machine.device.id} ${group.id}`,
        ).toBe(true);
        if (group.id.startsWith('processes-')) {
          expect(values).toEqual([...values].sort((a, b) => b! - a!));
        }
      }
    }
  });

  it('counts the time the laptop sleeps as not in use, not as outages', async () => {
    const laptop = await firstValueFrom(devices.availability('linux-laptop'));
    expect(laptop.kind).toBe('pc');
    expect(laptop.outages).toBeGreaterThan(0);
    const nas = await firstValueFrom(devices.availability('linux-nas'));
    expect(nas).toMatchObject({ kind: 'server', outages: 0 });
  });

  it('answers 503 for the device that is asleep', async () => {
    await expect(firstValueFrom(metrics.current('linux-laptop'))).rejects.toMatchObject({
      status: 503,
    });
  });

  it('ends the history of the device that is asleep at its last reading', async () => {
    const to = Math.floor(Date.now() / 1000);
    const history = await firstValueFrom(metrics.history(to - 60, to, 'linux-laptop'));
    expect(history.lastReading).toBeDefined();
    expect(history.to).toBe(history.lastReading! + 1);
    expect(history.to - history.from).toBe(60);
    expect(history.series.find((s) => s.metric === 'cpu')?.points.length).toBeGreaterThan(0);

    const live = await firstValueFrom(metrics.history(to - 60, to, 'linux-nas'));
    expect(live.lastReading).toBeUndefined();
    expect(live.to).toBe(to);
  });

  it('keeps the history within the retention and in at most 360 steps', async () => {
    const to = Math.floor(Date.now() / 1000);
    const history = await firstValueFrom(metrics.history(to - 365 * 86400, to, 'linux-nas'));
    expect(history.retentionDays).toBe(DEMO_RETENTION_DAYS);
    const points = history.series.find((s) => s.metric === 'disk:/srv/data')?.points ?? [];
    expect(points.length).toBeGreaterThan(0);
    expect(points.length).toBeLessThanOrEqual(360);
    expect(points[0].time).toBeGreaterThanOrEqual(to - DEMO_RETENTION_DAYS * 86400);
  });

  it("keeps the Wi-Fi quality's history but not the signal's, with labels the hub keeps whole", async () => {
    const s = await firstValueFrom(metrics.current('windows-laptop'));
    const items = s.extras?.find((g) => g.id === 'wifi')?.items ?? [];
    // The adapter's long description is cut, so what each value is stays in its label.
    expect(items.map((i) => i.label.slice(-12))).toEqual(['link quality', 'signal (dBm)']);
    for (const item of items) {
      expect(item.label.length).toBeLessThanOrEqual(80);
      expect(item.labels?.['de']?.length).toBeLessThanOrEqual(80);
    }
    const to = Math.floor(Date.now() / 1000);
    const history = await firstValueFrom(metrics.history(to - 3600, to, 'windows-laptop'));
    const wifi = history.series.map((s) => s.metric).filter((m) => m.startsWith('extra:wifi/'));
    expect(wifi).toEqual([expect.stringMatching(/-quality$/)]);
  });

  it('refuses to add or remove devices, and says why', async () => {
    const i18n = TestBed.inject(I18n);
    i18n.language.set('en-GB');
    const refused = await firstValueFrom(
      devices.add('NAS', '192.168.1.9:9393', 'server', 'password'),
    ).catch((error: unknown) => error);
    expect(problemMessage(refused, i18n)).toBe(
      "This is a demo, so devices can't be added, removed or changed.",
    );
    await expect(
      firstValueFrom(devices.remove('windows-pc', 'password', false)),
    ).rejects.toMatchObject({ status: 403 });
  });
});

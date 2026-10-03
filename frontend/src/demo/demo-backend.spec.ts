import { HttpBackend, provideHttpClient } from '@angular/common/http';
import { TestBed } from '@angular/core/testing';
import { firstValueFrom } from 'rxjs';

import { DeviceService, LOCAL_DEVICE, problemMessage } from '../app/devices/devices';
import { I18n } from '../app/i18n/i18n';
import { MetricsService } from '../app/metrics/metrics';
import { DEMO_RETENTION_DAYS, DemoBackend } from './demo-backend';
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
      // Windows has no load average and no temperature sensors the backend reads.
      expect(s.cpu.loadAverage === undefined).toBe(machine.os === 'windows');
      expect(s.temperatures.length === 0).toBe(machine.os === 'windows');
    }
  });

  it('answers 503 for the device that is asleep', async () => {
    await expect(firstValueFrom(metrics.current('linux-laptop'))).rejects.toMatchObject({
      status: 503,
    });
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

  it('refuses to add or remove devices, and says why', async () => {
    const i18n = TestBed.inject(I18n);
    i18n.language.set('en');
    const refused = await firstValueFrom(devices.add('NAS', '192.168.1.9:9393', 'password')).catch(
      (error: unknown) => error,
    );
    expect(problemMessage(refused, i18n)).toBe(
      "This is a demo, so devices can't be added or removed.",
    );
    await expect(
      firstValueFrom(devices.remove('windows-pc', 'password', false)),
    ).rejects.toMatchObject({ status: 403 });
  });
});

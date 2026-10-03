import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { DeviceService } from '../devices/devices';
import { History } from '../metrics/metrics';
import { translate } from '../i18n/i18n';
import { HistoryCharts, chartsOf, refreshIntervalMs } from './history';

const NOW = new Date('2026-10-02T20:00:00Z');
const NOW_SECONDS = NOW.getTime() / 1000;

function historyFor(from: number, to: number, retentionDays: number): History {
  return {
    from,
    to,
    stepSeconds: 240,
    retentionDays,
    series: [
      { metric: 'cpu', points: [{ time: from, value: 12.5 }] },
      { metric: 'memory', points: [{ time: from, value: 40 }] },
    ],
  };
}

describe('HistoryCharts', () => {
  let fixture: ComponentFixture<HistoryCharts>;
  let http: HttpTestingController;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
    TestBed.configureTestingModule({
      imports: [HistoryCharts],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(HistoryCharts);
  });

  afterEach(() => {
    http.verify();
    vi.useRealTimers();
  });

  /** Answers the pending history request and returns the range it asked for. */
  function respond(retentionDays = 30): { from: number; to: number } {
    fixture.detectChanges();
    vi.advanceTimersByTime(0);
    const request = http.expectOne((r) => r.url === '/api/history');
    const from = Number(request.request.params.get('from'));
    const to = Number(request.request.params.get('to'));
    request.flush(historyFor(from, to, retentionDays));
    fixture.detectChanges();
    return { from, to };
  }

  function element(): HTMLElement {
    return fixture.nativeElement as HTMLElement;
  }

  function labels(selector: string): string[] {
    return Array.from(element().querySelectorAll(selector)).map((b) => b.textContent?.trim() ?? '');
  }

  function click(selector: string, label: string): void {
    const buttons = Array.from(element().querySelectorAll<HTMLButtonElement>(selector));
    buttons.find((b) => b.textContent?.trim() === label)?.click();
  }

  const UNIT = '.units mat-chip-option button';
  const RANGE = '.ranges mat-chip-option button';

  it('shows the last 24 hours and refreshes them every minute', () => {
    expect(respond()).toEqual({ from: NOW_SECONDS - 86400, to: NOW_SECONDS });
    expect(element().textContent).toContain('CPU and memory');
    expect(labels(RANGE)).toEqual(['1 d', '7 d', '14 d', '30 d']);
    expect(element().querySelector('.ranges .mat-mdc-chip-selected')?.textContent?.trim()).toBe(
      '1 d',
    );

    vi.advanceTimersByTime(60_000);
    expect(respond().to).toBe(NOW_SECONDS + 60);
  });

  it('shows the longest range of a chosen unit, then the chosen range', () => {
    respond();

    click(UNIT, 'Minutes');
    expect(respond()).toEqual({ from: NOW_SECONDS - 30 * 60, to: NOW_SECONDS });
    expect(labels(RANGE)).toEqual(['1 min', '5 min', '10 min', '30 min']);

    click(UNIT, 'Hours');
    expect(respond()).toEqual({ from: NOW_SECONDS - 12 * 3600, to: NOW_SECONDS });
    expect(labels(RANGE)).toEqual(['1 h', '3 h', '6 h', '12 h']);

    click(UNIT, 'Minutes');
    respond();

    click(RANGE, '1 min');
    expect(respond()).toEqual({ from: NOW_SECONDS - 60, to: NOW_SECONDS });
    // Short ranges are read again every 5 seconds.
    vi.advanceTimersByTime(5_000);
    expect(respond().to).toBe(NOW_SECONDS + 5);
  });

  it('skips a refresh while the previous answer is still on its way', () => {
    respond();
    vi.advanceTimersByTime(60_000);
    const slow = http.expectOne((r) => r.url === '/api/history');

    vi.advanceTimersByTime(60_000);
    http.expectNone((r) => r.url === '/api/history');

    slow.flush(historyFor(NOW_SECONDS - 86400 + 60, NOW_SECONDS + 60, 30));
    vi.advanceTimersByTime(60_000);
    expect(respond().to).toBe(NOW_SECONDS + 180);
  });

  it('offers all data when more than 30 days are kept', () => {
    respond();
    expect(labels(UNIT)).toEqual(['Minutes', 'Hours', 'Days']);

    vi.advanceTimersByTime(60_000);
    respond(90);
    expect(labels(UNIT)).toEqual(['Minutes', 'Hours', 'Days', 'All']);

    click(UNIT, 'All');
    expect(respond(90).from).toBe(NOW_SECONDS + 60 - 90 * 86400);
    expect(labels(RANGE)).toEqual([]);
  });

  it('offers only ranges within the retention', () => {
    respond(7);
    expect(labels(UNIT)).toEqual(['Minutes', 'Hours', 'Days']);
    expect(labels(RANGE)).toEqual(['1 d', '7 d']);
  });

  it('reads the history of the picked device', () => {
    respond();

    TestBed.inject(DeviceService).selectedId.set('living-room-pi');
    fixture.detectChanges();
    vi.advanceTimersByTime(0);

    const request = http.expectOne((r) => r.url === '/api/history');
    expect(request.request.params.get('device')).toBe('living-room-pi');
    request.flush(historyFor(NOW_SECONDS - 86400, NOW_SECONDS, 30));
  });
});

describe('refreshIntervalMs', () => {
  it('refreshes short ranges every 5 seconds, a day every minute and longer ranges every 5 minutes', () => {
    expect(refreshIntervalMs(60)).toBe(5_000);
    expect(refreshIntervalMs(30 * 60)).toBe(5_000);
    expect(refreshIntervalMs(60 * 60)).toBe(60_000);
    expect(refreshIntervalMs(86400)).toBe(60_000);
    expect(refreshIntervalMs(7 * 86400)).toBe(300_000);
    expect(refreshIntervalMs(30 * 86400)).toBe(300_000);
  });
});

describe('chartsOf', () => {
  it('groups metrics into charts and adds up the network cards', () => {
    const charts = chartsOf(
      [
        { metric: 'cpu', points: [{ time: 0, value: 10 }] },
        { metric: 'disk:/', points: [{ time: 0, value: 30 }] },
        { metric: 'network.receive:eth0', points: [{ time: 0, value: 100 }] },
        { metric: 'network.receive:wlan0', points: [{ time: 0, value: 50 }] },
        { metric: 'network.send:eth0', points: [{ time: 0, value: 7 }] },
      ],
      (key, params) => translate('en', key, params),
    );

    expect(charts.map((c) => c.title)).toEqual(['CPU and memory', 'Network', 'Disks']);
    expect(charts[1].lines[0].points).toEqual([{ time: 0, value: 150 }]);
    expect(charts[2].lines[0].label).toBe('/');
  });
});

describe('chartsOf with swap and disk activity', () => {
  it('adds swap to the CPU and memory chart and charts disk speeds', () => {
    const charts = chartsOf(
      [
        { metric: 'cpu', points: [{ time: 0, value: 10 }] },
        { metric: 'disk.read:/', points: [{ time: 0, value: 2048 }] },
        { metric: 'disk.write:/', points: [{ time: 0, value: 512 }] },
        { metric: 'swap', points: [{ time: 0, value: 5 }] },
      ],
      (key, params) => translate('en', key, params),
    );

    expect(charts.map((c) => c.title)).toEqual(['CPU and memory', 'Disk activity']);
    expect(charts[0].lines.map((l) => l.label)).toEqual(['CPU', 'Memory', 'Swap']);
    expect(charts[1].unit).toBe('bytesPerSecond');
    expect(charts[1].lines.map((l) => l.label)).toEqual(['/ read', '/ written']);
  });

  it('has no swap line for a machine without swap space', () => {
    const charts = chartsOf([{ metric: 'cpu', points: [{ time: 0, value: 10 }] }], (key, params) =>
      translate('en', key, params),
    );
    expect(charts[0].lines.map((l) => l.label)).toEqual(['CPU', 'Memory']);
  });
});

describe('chartsOf with a battery', () => {
  it('charts the battery charge', () => {
    const charts = chartsOf(
      [
        { metric: 'cpu', points: [{ time: 0, value: 10 }] },
        { metric: 'battery', points: [{ time: 0, value: 80 }] },
      ],
      (key, params) => translate('en', key, params),
    );
    expect(charts.map((c) => c.title)).toEqual(['CPU and memory', 'Battery']);
    expect(charts[1].lines[0].label).toBe('Charge');
  });
});

describe('chartsOf with GPUs', () => {
  it('shows the usage and memory of each GPU in one chart', () => {
    const charts = chartsOf(
      [
        { metric: 'cpu', points: [{ time: 0, value: 10 }] },
        { metric: 'gpu:AMD GPU', points: [{ time: 0, value: 70 }] },
        { metric: 'gpu.memory:AMD GPU', points: [{ time: 0, value: 25 }] },
      ],
      (key, params) => translate('en', key, params),
    );

    expect(charts.map((c) => c.title)).toEqual(['CPU and memory', 'GPU']);
    expect(charts[1].lines.map((l) => l.label)).toEqual(['AMD GPU', 'AMD GPU memory']);
  });
});

import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { DeviceService } from '../devices/devices';
import { History } from '../metrics/metrics';
import { HistoryCharts, chartsOf } from './history';

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

  function rangeButtons(): HTMLButtonElement[] {
    const element = fixture.nativeElement as HTMLElement;
    return Array.from(element.querySelectorAll('mat-button-toggle button'));
  }

  function selectRange(label: string): void {
    rangeButtons()
      .find((b) => b.textContent?.trim() === label)
      ?.click();
  }

  it('shows the last 24 hours and refreshes them every minute', () => {
    expect(respond()).toEqual({ from: NOW_SECONDS - 86400, to: NOW_SECONDS });
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('CPU and memory');

    vi.advanceTimersByTime(60_000);
    expect(respond().to).toBe(NOW_SECONDS + 60);
  });

  it('refreshes short ranges every 5 seconds', () => {
    respond();

    selectRange('1 min');
    expect(respond()).toEqual({ from: NOW_SECONDS - 60, to: NOW_SECONDS });
    vi.advanceTimersByTime(5_000);
    expect(respond().to).toBe(NOW_SECONDS + 5);
  });

  it('offers ranges up to 30 days, and all data when more is kept', () => {
    respond();
    const labels = () => rangeButtons().map((b) => b.textContent?.trim());
    expect(labels()).toEqual([
      '1 min',
      '5 min',
      '10 min',
      '30 min',
      '1 h',
      '6 h',
      '24 h',
      '7 d',
      '30 d',
    ]);

    vi.advanceTimersByTime(60_000);
    respond(90);
    expect(labels().at(-1)).toBe('All');

    selectRange('All');
    expect(respond(90).from).toBe(NOW_SECONDS + 60 - 90 * 86400);
  });

  it('offers only ranges within the retention', () => {
    respond(1);
    expect(rangeButtons().map((b) => b.textContent?.trim())).not.toContain('7 d');
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

describe('chartsOf', () => {
  it('groups metrics into charts and adds up the network cards', () => {
    const charts = chartsOf([
      { metric: 'cpu', points: [{ time: 0, value: 10 }] },
      { metric: 'disk:/', points: [{ time: 0, value: 30 }] },
      { metric: 'network.receive:eth0', points: [{ time: 0, value: 100 }] },
      { metric: 'network.receive:wlan0', points: [{ time: 0, value: 50 }] },
      { metric: 'network.send:eth0', points: [{ time: 0, value: 7 }] },
    ]);

    expect(charts.map((c) => c.title)).toEqual(['CPU and memory', 'Network', 'Disks']);
    expect(charts[1].lines[0].points).toEqual([{ time: 0, value: 150 }]);
    expect(charts[2].lines[0].label).toBe('/');
  });
});

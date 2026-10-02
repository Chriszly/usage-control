import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { History } from '../metrics/metrics';
import { HISTORY_REFRESH_INTERVAL_MS, HistoryCharts, chartsOf } from './history';

const NOW = new Date('2026-10-02T20:00:00Z');
const NOW_SECONDS = NOW.getTime() / 1000;

function historyFor(from: number, to: number): History {
  return {
    from,
    to,
    stepSeconds: 240,
    retentionDays: 30,
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
  function respond(): { from: number; to: number } {
    fixture.detectChanges();
    vi.advanceTimersByTime(0);
    const request = http.expectOne((r) => r.url === '/api/history');
    const from = Number(request.request.params.get('from'));
    const to = Number(request.request.params.get('to'));
    request.flush(historyFor(from, to));
    fixture.detectChanges();
    return { from, to };
  }

  function element(): HTMLElement {
    return fixture.nativeElement as HTMLElement;
  }

  function button(label: string): HTMLButtonElement {
    const buttons = Array.from(element().querySelectorAll('button'));
    return buttons.find((b) => b.textContent?.includes(label)) as HTMLButtonElement;
  }

  it('shows the last 24 hours and refreshes them every minute', () => {
    expect(respond()).toEqual({ from: NOW_SECONDS - 86400, to: NOW_SECONDS });
    expect(element().textContent).toContain('CPU and memory');

    vi.advanceTimersByTime(HISTORY_REFRESH_INTERVAL_MS);
    expect(respond().to).toBe(NOW_SECONDS + 60);
  });

  it('scrolls back one range and returns to now', () => {
    respond();

    button('Earlier').click();
    expect(respond()).toEqual({ from: NOW_SECONDS - 2 * 86400, to: NOW_SECONDS - 86400 });
    // Showing older values, it no longer refreshes.
    vi.advanceTimersByTime(HISTORY_REFRESH_INTERVAL_MS);
    http.expectNone('/api/history');

    button('Later').click();
    expect(respond().to).toBe(NOW_SECONDS);

    button('Now').click();
    expect(respond().to).toBe(NOW_SECONDS + HISTORY_REFRESH_INTERVAL_MS / 1000);
    expect(button('Later').disabled).toBe(true);
  });

  it('shows the chosen range', () => {
    respond();

    const toggle = Array.from(element().querySelectorAll('mat-button-toggle button')).find((b) =>
      b.textContent?.includes('7 d'),
    ) as HTMLButtonElement;
    toggle.click();

    expect(respond().from).toBe(NOW_SECONDS - 7 * 86400);
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

import { DatePipe } from '@angular/common';
import { Component, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatCardModule } from '@angular/material/card';
import { catchError, map, of, switchMap, timer } from 'rxjs';

import { History, MetricsService, Point, Series } from '../metrics/metrics';
import { ChartLine, ChartUnit, LineChart } from './line-chart';

/** How often the history is read again while it shows the latest values. */
export const HISTORY_REFRESH_INTERVAL_MS = 60_000;

/** The time ranges a chart can show at once. */
export const RANGES = [
  { label: $localize`:Range of one hour@@history.range1h:1 h`, seconds: 3600 },
  { label: $localize`:Range of six hours@@history.range6h:6 h`, seconds: 6 * 3600 },
  { label: $localize`:Range of one day@@history.range24h:24 h`, seconds: 86400 },
  { label: $localize`:Range of seven days@@history.range7d:7 d`, seconds: 7 * 86400 },
  { label: $localize`:Range of 30 days@@history.range30d:30 d`, seconds: 30 * 86400 },
];

/** How many days the backend keeps by default, used until it says otherwise. */
const DEFAULT_RETENTION_DAYS = 30;

interface Chart {
  title: string;
  unit: ChartUnit;
  max?: number;
  lines: ChartLine[];
}

/**
 * Shows the machine's usage over time as charts. The range picks how much time
 * the charts show; "Earlier" and "Later" scroll through the history, as far
 * back as the backend keeps it.
 */
@Component({
  selector: 'app-history',
  imports: [DatePipe, LineChart, MatButtonModule, MatButtonToggleModule, MatCardModule],
  templateUrl: './history.html',
  styleUrl: './history.css',
})
export class HistoryCharts {
  private readonly metrics = inject(MetricsService);

  /** The length of the shown time, in seconds. */
  protected readonly span = signal(86400);
  /** The end of the shown time in Unix seconds, or null to show the latest values. */
  protected readonly end = signal<number | null>(null);

  protected readonly history = signal<History | null>(null);
  protected readonly unreachable = signal(false);

  protected readonly retentionDays = computed(
    () => this.history()?.retentionDays ?? DEFAULT_RETENTION_DAYS,
  );
  protected readonly ranges = computed(() =>
    RANGES.filter((range) => range.seconds <= this.retentionDays() * 86400),
  );
  /** Whether older values may still be kept, so scrolling back can show them. */
  protected readonly canGoEarlier = computed(() => {
    const history = this.history();
    return history !== null && history.from > nowSeconds() - this.retentionDays() * 86400 + 60;
  });

  protected readonly charts = computed(() => {
    const history = this.history();
    return history ? chartsOf(history.series) : [];
  });

  constructor() {
    const shown = computed(() => ({ span: this.span(), end: this.end() }));
    toObservable(shown)
      .pipe(
        // While it shows the latest values, it moves along with the time.
        switchMap((s) =>
          s.end === null ? timer(0, HISTORY_REFRESH_INTERVAL_MS).pipe(map(() => s)) : of(s),
        ),
        switchMap(({ span, end }) => {
          const to = end ?? nowSeconds();
          return this.metrics.history(to - span, to).pipe(catchError(() => of(null)));
        }),
        takeUntilDestroyed(),
      )
      .subscribe((history) => {
        this.unreachable.set(history === null);
        if (history) {
          this.history.set(history);
        }
      });
  }

  protected selectRange(seconds: number): void {
    this.span.set(seconds);
  }

  protected earlier(): void {
    const from = this.history()?.from ?? nowSeconds() - this.span();
    this.end.set(from);
  }

  protected later(): void {
    const end = this.end();
    if (end === null) {
      return;
    }
    const next = end + this.span();
    this.end.set(next >= nowSeconds() ? null : next);
  }

  protected latest(): void {
    this.end.set(null);
  }
}

function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

/**
 * Groups the stored metrics into charts: CPU and memory, temperatures, network
 * speed and disks. Charts without values are left out.
 */
export function chartsOf(series: Series[]): Chart[] {
  const named = (kind: string) =>
    series
      .filter((s) => s.metric.startsWith(kind + ':'))
      .map((s) => ({ label: s.metric.slice(kind.length + 1), points: s.points }));
  const metric = (name: string) => series.find((s) => s.metric === name)?.points ?? [];

  const charts: Chart[] = [
    {
      title: $localize`:Chart title@@history.cpuAndMemory:CPU and memory`,
      unit: 'percent',
      max: 100,
      lines: [
        { label: $localize`:Line in a chart@@history.cpu:CPU`, points: metric('cpu') },
        { label: $localize`:Line in a chart@@history.memory:Memory`, points: metric('memory') },
      ],
    },
    {
      title: $localize`:Chart title@@history.temperature:Temperature`,
      unit: 'celsius',
      lines: named('temperature'),
    },
    {
      title: $localize`:Chart title@@history.network:Network`,
      unit: 'bytesPerSecond',
      lines: [
        {
          label: $localize`:Network traffic received@@history.received:Received`,
          points: sum(named('network.receive')),
        },
        {
          label: $localize`:Network traffic sent@@history.sent:Sent`,
          points: sum(named('network.send')),
        },
      ],
    },
    {
      title: $localize`:Chart title@@history.disks:Disks`,
      unit: 'percent',
      max: 100,
      lines: named('disk'),
    },
  ];
  return charts.filter((chart) => chart.lines.some((line) => line.points.length > 0));
}

/** Adds up the values of several lines at each time, such as the traffic of all network cards. */
function sum(lines: ChartLine[]): Point[] {
  const totals = new Map<number, number>();
  for (const line of lines) {
    for (const point of line.points) {
      totals.set(point.time, (totals.get(point.time) ?? 0) + point.value);
    }
  }
  return [...totals].map(([time, value]) => ({ time, value })).sort((a, b) => a.time - b.time);
}

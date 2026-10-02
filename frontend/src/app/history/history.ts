import { DatePipe } from '@angular/common';
import { Component, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatCardModule } from '@angular/material/card';
import { catchError, map, of, switchMap, tap, timer } from 'rxjs';

import { DeviceService } from '../devices/devices';
import { I18n, TextParams } from '../i18n/i18n';
import { MessageKey } from '../i18n/messages/en';
import { History, MetricsService, Point, Series } from '../metrics/metrics';
import { ChartLine, ChartUnit, LineChart } from './line-chart';

/** A time range a chart can show, always ending now. */
export interface Range {
  label: MessageKey;
  seconds: number;
}

/** A group of ranges picked in the first row; its ranges are picked in the second. */
export interface RangeUnit {
  id: 'minutes' | 'hours' | 'days' | 'all';
  label: MessageKey;
  ranges: Range[];
}

/** The fixed ranges, grouped by unit. Longer retention adds the "All" unit. */
export const UNITS: RangeUnit[] = [
  {
    id: 'minutes',
    label: 'history.unitMinutes',
    ranges: [
      { label: 'history.range1m', seconds: 60 },
      { label: 'history.range5m', seconds: 5 * 60 },
      { label: 'history.range10m', seconds: 10 * 60 },
      { label: 'history.range30m', seconds: 30 * 60 },
    ],
  },
  {
    id: 'hours',
    label: 'history.unitHours',
    ranges: [
      { label: 'history.range1h', seconds: 3600 },
      { label: 'history.range3h', seconds: 3 * 3600 },
      { label: 'history.range6h', seconds: 6 * 3600 },
      { label: 'history.range12h', seconds: 12 * 3600 },
    ],
  },
  {
    id: 'days',
    label: 'history.unitDays',
    ranges: [
      { label: 'history.range1d', seconds: 86400 },
      { label: 'history.range7d', seconds: 7 * 86400 },
      { label: 'history.range14d', seconds: 14 * 86400 },
      { label: 'history.range30d', seconds: 30 * 86400 },
    ],
  },
];

/** The longest fixed range; when more is kept, the "All" unit shows everything. */
const LONGEST_RANGE_SECONDS = 30 * 86400;

/** How many days the backend keeps by default, used until it says otherwise. */
const DEFAULT_RETENTION_DAYS = 30;

/**
 * How often a range is read again: every 5 seconds for ranges of up to 30
 * minutes, which the backend has readings every 5 seconds for, else every minute.
 */
export function refreshIntervalMs(spanSeconds: number): number {
  return spanSeconds <= 30 * 60 ? 5_000 : 60_000;
}

interface Chart {
  title: string;
  unit: ChartUnit;
  max?: number;
  lines: ChartLine[];
}

/**
 * Shows the machine's usage over the latest stretch of time as charts. A unit
 * (minutes, hours, days or all) and then a range of that unit pick how long
 * that stretch is, up to everything the backend keeps.
 */
@Component({
  selector: 'app-history',
  imports: [DatePipe, LineChart, MatButtonModule, MatButtonToggleModule, MatCardModule],
  templateUrl: './history.html',
  styleUrl: './history.css',
})
export class HistoryCharts {
  private readonly metrics = inject(MetricsService);
  private readonly devices = inject(DeviceService);
  protected readonly i18n = inject(I18n);

  /** The length of the shown time, in seconds. */
  protected readonly span = signal(86400);

  protected readonly history = signal<History | null>(null);
  protected readonly unreachable = signal(false);

  /** The units with at least one range within the retention. */
  protected readonly units = computed((): RangeUnit[] => {
    const retention = (this.history()?.retentionDays ?? DEFAULT_RETENTION_DAYS) * 86400;
    const units = UNITS.map((unit) => ({
      ...unit,
      ranges: unit.ranges.filter((range) => range.seconds <= retention),
    })).filter((unit) => unit.ranges.length > 0);
    if (retention > LONGEST_RANGE_SECONDS) {
      const label = 'history.rangeAll' as const;
      units.push({ id: 'all', label, ranges: [{ label, seconds: retention }] });
    }
    return units;
  });

  /** The unit the shown range belongs to. */
  protected readonly unit = computed(
    () =>
      this.units().find((unit) => unit.ranges.some((range) => range.seconds === this.span())) ??
      null,
  );

  /** Short ranges show seconds too. */
  protected readonly dateFormat = computed(() =>
    this.span() <= 30 * 60 ? 'EEE d MMM, HH:mm:ss' : 'EEE d MMM, HH:mm',
  );

  protected readonly charts = computed(() => {
    const history = this.history();
    return history ? chartsOf(history.series, (key, params) => this.i18n.t(key, params)) : [];
  });

  constructor() {
    let shownDevice = this.devices.selectedId();
    toObservable(computed(() => ({ span: this.span(), device: this.devices.selectedId() })))
      .pipe(
        // Another device's charts are not shown while the picked one's load.
        tap(({ device }) => {
          if (device !== shownDevice) {
            shownDevice = device;
            this.history.set(null);
          }
        }),
        switchMap(({ span, device }) =>
          timer(0, refreshIntervalMs(span)).pipe(map(() => ({ span, device }))),
        ),
        switchMap(({ span, device }) => {
          const to = nowSeconds();
          return this.metrics.history(to - span, to, device).pipe(catchError(() => of(null)));
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

  /** Shows the longest range of a unit, so switching units zooms in or out. */
  protected selectUnit(id: RangeUnit['id']): void {
    const unit = this.units().find((u) => u.id === id);
    if (unit) {
      this.span.set(unit.ranges[unit.ranges.length - 1].seconds);
    }
  }

  protected selectRange(seconds: number): void {
    this.span.set(seconds);
  }
}

function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

/**
 * Groups the stored metrics into charts: CPU and memory, GPUs, temperatures,
 * network speed and disks, with titles and labels in the language of t. Charts without
 * values are left out.
 */
export function chartsOf(
  series: Series[],
  t: (key: MessageKey, params?: TextParams) => string,
): Chart[] {
  const named = (kind: string) =>
    series
      .filter((s) => s.metric.startsWith(kind + ':'))
      .map((s) => ({ label: s.metric.slice(kind.length + 1), points: s.points }));
  const metric = (name: string) => series.find((s) => s.metric === name)?.points ?? [];

  const charts: Chart[] = [
    {
      title: t('history.cpuAndMemory'),
      unit: 'percent',
      max: 100,
      lines: [
        { label: t('history.cpu'), points: metric('cpu') },
        { label: t('history.memory'), points: metric('memory') },
      ],
    },
    {
      title: t('history.gpu'),
      unit: 'percent',
      max: 100,
      lines: [
        ...named('gpu'),
        ...named('gpu.memory').map((line) => ({
          ...line,
          label: t('history.gpuMemory', { name: line.label }),
        })),
      ],
    },
    {
      title: t('history.temperature'),
      unit: 'celsius',
      lines: named('temperature'),
    },
    {
      title: t('history.network'),
      unit: 'bytesPerSecond',
      lines: [
        {
          label: t('history.received'),
          points: sum(named('network.receive')),
        },
        {
          label: t('history.sent'),
          points: sum(named('network.send')),
        },
      ],
    },
    {
      title: t('history.disks'),
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

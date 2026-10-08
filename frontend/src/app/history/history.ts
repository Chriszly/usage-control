import { DatePipe, formatDate } from '@angular/common';
import { Component, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { catchError, exhaustMap, of, switchMap, tap } from 'rxjs';

import { HubConnection } from '../connection/connection';
import { DeviceService } from '../devices/devices';
import { I18n, LanguageCode, TextParams } from '../i18n/i18n';
import { MessageKey } from '../i18n/messages/en';
import { EvenColumns } from '../layout/even-columns';
import { ExtraInfo, localized } from '../metrics/extras';
import { History, MetricsService, Point, Series } from '../metrics/metrics';
import { PageVisibility } from '../page-visibility';
import { ChartLine, ChartUnit, LineChart, dateTimeFormat, needsYear } from './line-chart';

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

/** The fixed ranges, grouped by unit. Retention longer than the longest of them adds the "All" unit. */
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

/** How many days the backend keeps by default, used until it says otherwise. */
const DEFAULT_RETENTION_DAYS = 30;

/**
 * How often a range is read again: every 5 seconds for ranges of up to 30
 * minutes, which the backend has readings every 5 seconds for; every minute
 * up to a day, when the backend stores a new average; and every 5 minutes
 * above that, where a step is hours long and a minute changes nothing visible.
 */
export function refreshIntervalMs(spanSeconds: number): number {
  if (spanSeconds <= 30 * 60) {
    return 5_000;
  }
  return spanSeconds <= 86400 ? 60_000 : 300_000;
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
 * that stretch is, up to everything the backend keeps. For a device that is
 * not answering, the backend moves the stretch back to end at its last
 * reading, and a notice says the charts are not live.
 */
@Component({
  selector: 'app-history',
  imports: [DatePipe, EvenColumns, LineChart],
  templateUrl: './history.html',
  styleUrl: './history.css',
})
export class HistoryCharts {
  private readonly metrics = inject(MetricsService);
  private readonly devices = inject(DeviceService);
  private readonly page = inject(PageVisibility);
  protected readonly i18n = inject(I18n);
  /** While the hub does not answer, the banner across the page says the charts are not live, and they fade. */
  protected readonly connection = inject(HubConnection);

  /** The length of the shown time, in seconds; opens on the last 30 minutes. */
  protected readonly span = signal(30 * 60);

  protected readonly history = signal<History | null>(null);
  protected readonly unreachable = signal(false);
  /**
   * How long the backend keeps history, from its last answer; the hub keeps
   * every device's for the same time, so a device switch does not reset it.
   */
  private readonly retentionDays = signal(DEFAULT_RETENTION_DAYS);

  /**
   * The units with at least one range within the retention, and "All" when
   * more is kept than the longest of those ranges shows.
   */
  protected readonly units = computed((): RangeUnit[] => {
    const retention = this.retentionDays() * 86400;
    const units = UNITS.map((unit) => ({
      ...unit,
      ranges: unit.ranges.filter((range) => range.seconds <= retention),
    })).filter((unit) => unit.ranges.length > 0);
    const longest = Math.max(...units.flatMap((unit) => unit.ranges.map((range) => range.seconds)));
    if (retention > longest) {
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

  /** Short ranges show seconds too, and long ranges or ranges into another year the year. */
  protected readonly dateFormat = computed(() => {
    const history = this.history();
    const year = history !== null && needsYear(history.from, history.to);
    return this.i18n.t(dateTimeFormat(this.span() <= 30 * 60, year));
  });

  /** For a device that is not answering, when the shown data ends, in the page's language. */
  protected readonly lastReading = computed(() => {
    const last = this.history()?.lastReading;
    return last ? formatDate(last * 1000, 'short', this.i18n.language()) : null;
  });

  protected readonly charts = computed(() => {
    const history = this.history();
    if (!history) {
      return [];
    }
    const language = this.i18n.language();
    return [
      ...chartsOf(history.series, (key, params) => this.i18n.t(key, params)),
      ...extraChartsOf(history.series, history.extras ?? {}, language),
    ];
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
            this.unreachable.set(false);
          }
        }),
        // A tick while the previous answer is still on its way is skipped; long
        // ranges take the backend a moment.
        switchMap(({ span, device }) =>
          this.page.ticks(refreshIntervalMs(span)).pipe(
            exhaustMap(() => {
              const to = nowSeconds();
              return this.metrics.history(to - span, to, device).pipe(catchError(() => of(null)));
            }),
          ),
        ),
        takeUntilDestroyed(),
      )
      .subscribe((history) => {
        this.unreachable.set(history === null);
        if (history) {
          this.history.set(history);
          if (history.retentionDays) {
            this.retentionDays.set(history.retentionDays);
            // A range picked before the backend said how long it keeps history
            // may be longer than that; the longest range offered stands in for it.
            if (!this.unit()) {
              this.span.set(this.longestRange());
            }
          }
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

  private longestRange(): number {
    return Math.max(...this.units().flatMap((unit) => unit.ranges.map((range) => range.seconds)));
  }
}

function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

/**
 * Groups the stored metrics into charts: CPU, memory and swap, battery, GPUs,
 * temperatures, network speed, disk usage and disk activity, with titles and
 * labels in the language of t. Charts without values are left out.
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
        // Machines without swap space have no swap line.
        ...(metric('swap').length > 0
          ? [{ label: t('history.swap'), points: metric('swap') }]
          : []),
      ],
    },
    {
      title: t('history.battery'),
      unit: 'percent',
      max: 100,
      lines: [{ label: t('history.charge'), points: metric('battery') }],
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
    {
      title: t('history.diskActivity'),
      unit: 'bytesPerSecond',
      lines: [
        ...named('disk.read').map((line) => ({
          ...line,
          label: t('history.diskRead', { path: line.label }),
        })),
        ...named('disk.write').map((line) => ({
          ...line,
          label: t('history.diskWritten', { path: line.label }),
        })),
      ],
    },
  ];
  return charts.filter((chart) => chart.lines.some((line) => line.points.length > 0));
}

/**
 * Draws the extras: one chart per group and unit, titled and labelled as the
 * device describes them, in the page's language. Series without a description
 * are left out.
 */
export function extraChartsOf(
  series: Series[],
  extras: Record<string, ExtraInfo>,
  language: LanguageCode,
): Chart[] {
  const charts = new Map<string, Chart>();
  for (const s of series) {
    const info = extras[s.metric];
    if (!info || info.unit === 'text' || s.points.length === 0) {
      continue;
    }
    const group = s.metric.slice(0, s.metric.indexOf('/'));
    const key = `${group} ${info.unit}`;
    let chart = charts.get(key);
    if (!chart) {
      chart = {
        title: localized(info.title, info.titles, language),
        unit: info.unit,
        max: info.unit === 'percent' ? 100 : undefined,
        lines: [],
      };
      charts.set(key, chart);
    }
    chart.lines.push({ label: localized(info.label, info.labels, language), points: s.points });
  }
  // A chart of one line has no legend, so its title names the value.
  return [...charts.values()].map((chart) =>
    chart.lines.length === 1
      ? { ...chart, title: `${chart.title} · ${chart.lines[0].label}` }
      : chart,
  );
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

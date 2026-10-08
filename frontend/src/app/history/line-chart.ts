import { DatePipe, NgTemplateOutlet, formatDate } from '@angular/common';
import { Component, computed, inject, input, signal } from '@angular/core';

import { I18n } from '../i18n/i18n';
import { MessageKey } from '../i18n/messages/en';
import { ExtraUnit, formatExtra } from '../metrics/extras';
import { Point } from '../metrics/metrics';

/** What the values of a chart are: any unit an extra can have, but text. */
export type ChartUnit = Exclude<ExtraUnit, 'text'>;

/** One line of a chart, such as the CPU usage over time. */
export interface ChartLine {
  label: string;
  points: Point[];
}

/** The size of the drawing in SVG units; it is stretched to the chart's size. */
const WIDTH = 1000;
const HEIGHT = 100;

/** How many colors the lines take in turn; lines after them are dashed. */
const COLORS = 8;

/**
 * Draws lines over time, from `from` to `to` (Unix seconds), with one point
 * per `step` seconds. Moving the pointer over it shows every line's value at
 * that time. Where values are missing, such as while the machine was off,
 * the line has a gap.
 */
@Component({
  selector: 'app-line-chart',
  imports: [DatePipe, NgTemplateOutlet],
  templateUrl: './line-chart.html',
  styleUrl: './line-chart.css',
})
export class LineChart {
  protected readonly i18n = inject(I18n);

  /** What the chart shows, such as "CPU and memory", for its text alternative. */
  readonly chartTitle = input.required<string>();
  readonly lines = input.required<ChartLine[]>();
  readonly from = input.required<number>();
  readonly to = input.required<number>();
  readonly step = input.required<number>();
  readonly unit = input.required<ChartUnit>();
  /** The top of the y axis; when not set, it fits the highest value. */
  readonly max = input<number>();

  protected readonly width = WIDTH;
  protected readonly height = HEIGHT;

  protected readonly top = computed(() => {
    const fixed = this.max();
    if (fixed !== undefined) {
      return fixed;
    }
    const highest = Math.max(0, ...this.lines().flatMap((line) => line.points.map((p) => p.value)));
    return niceCeiling(highest, this.unit());
  });

  protected readonly paths = computed(() =>
    this.lines().map((line) => this.path(line.points, this.top())),
  );

  /** Whether the chart's dates need their year, so dates a year apart are told apart. */
  private readonly withYear = computed(() => needsYear(this.from(), this.to()));

  /**
   * The date format of the time axis: the time of day, with the day for longer
   * ranges and the year when it is needed.
   */
  protected readonly timeFormat = computed(() => {
    const span = this.to() - this.from();
    if (span <= 30 * 60) {
      return this.i18n.t('format.timeSeconds');
    }
    if (span <= 86400) {
      return this.i18n.t('format.time');
    }
    if (span <= 8 * 86400) {
      return this.i18n.t('format.weekdayTime');
    }
    return this.i18n.t(this.withYear() ? 'format.dayMonthYear' : 'format.dayMonth');
  });

  /** For screen readers: what the chart shows, of which time, and each line's latest value. */
  protected readonly summary = computed(() => {
    const language = this.i18n.language();
    const format = this.i18n.t(dateTimeFormat(false, this.withYear()));
    const lines = this.lines().map((line) => {
      const latest = line.points.at(-1);
      return latest
        ? this.i18n.t('history.chartLineLatest', {
            label: line.label,
            value: this.format(latest.value),
          })
        : this.i18n.t('history.chartLineNoData', { label: line.label });
    });
    return this.i18n.t('history.chartSummary', {
      title: this.chartTitle(),
      from: formatDate(this.from() * 1000, format, language),
      to: formatDate(this.to() * 1000, format, language),
      lines: new Intl.ListFormat(language).format(lines),
    });
  });

  /** The start of the step under the pointer, or null when the pointer is elsewhere. */
  protected readonly hoverTime = signal<number | null>(null);

  /** The date format of the tooltip, with seconds when a step is shorter than a minute. */
  protected readonly readoutFormat = computed(() =>
    this.i18n.t(dateTimeFormat(this.step() < 60, this.withYear())),
  );

  protected readonly readout = computed(() => {
    const time = this.hoverTime();
    if (time === null) {
      return null;
    }
    return {
      time,
      left: this.fraction(time + this.step() / 2) * 100,
      values: this.lines().map((line) => line.points.find((p) => p.time === time)?.value ?? null),
    };
  });

  /** The color of a line by its place, dashed once every color is taken. */
  protected seriesClass(index: number): string {
    return `series-${index % COLORS}` + (index >= COLORS ? ' dashed' : '');
  }

  /** A value with the chart's unit, in the page's language. */
  protected format(value: number): string {
    return formatExtra(value, this.unit(), this.i18n.language());
  }

  protected onPointerMove(event: PointerEvent): void {
    const area = (event.currentTarget as HTMLElement).getBoundingClientRect();
    const fraction = Math.min(1, Math.max(0, (event.clientX - area.left) / area.width));
    const time = this.from() + fraction * (this.to() - this.from());
    const step = this.step();
    // The right edge belongs to the last step, which starts before `to`.
    this.hoverTime.set(Math.floor(Math.min(time, this.to() - 1) / step) * step);
  }

  protected onPointerLeave(): void {
    this.hoverTime.set(null);
  }

  /** Where a time is on the time axis, from 0 (start) to 1 (end). */
  private fraction(time: number): number {
    return (time - this.from()) / (this.to() - this.from());
  }

  /** The SVG path of a line, starting a new segment after a gap. */
  private path(points: Point[], top: number): string {
    const step = this.step();
    let path = '';
    let previous: number | null = null;
    for (const point of points) {
      // Each point is the average of its step, so it is drawn in the step's middle.
      const x = this.fraction(point.time + step / 2) * WIDTH;
      const y = HEIGHT - (Math.min(point.value, top) / top) * HEIGHT;
      const gap = previous === null || point.time - previous > 2 * step;
      // A lone point gets a line of no length, which the round line caps show as a dot.
      path += gap ? `M${x.toFixed(1)} ${y.toFixed(1)}h0` : `L${x.toFixed(1)} ${y.toFixed(1)}`;
      previous = point.time;
    }
    return path;
  }
}

/**
 * Whether dates from `from` to `to` (Unix seconds) need their year: when they
 * span most of a year, or the new year falls between them.
 */
export function needsYear(from: number, to: number): boolean {
  return (
    to - from > 300 * 86400 ||
    new Date(from * 1000).getFullYear() !== new Date(to * 1000).getFullYear()
  );
}

/** The key of the date and time format, with seconds and the year as asked. */
export function dateTimeFormat(seconds: boolean, year: boolean): MessageKey {
  if (year) {
    return seconds ? 'format.dateTimeSecondsYear' : 'format.dateTimeYear';
  }
  return seconds ? 'format.dateTimeSeconds' : 'format.dateTime';
}

/**
 * A round number at or above value for the top of the y axis: the next ten
 * percent or degrees; the next power of two bytes so the axis reads 1 MiB/s
 * instead of 0.95 MiB/s; else the next 1, 2 or 5 times a power of ten.
 */
export function niceCeiling(value: number, unit: ChartUnit): number {
  if (unit === 'bytesPerSecond' || unit === 'bytes') {
    return 2 ** Math.ceil(Math.log2(Math.max(value, 1024)));
  }
  if (unit === 'percent' || unit === 'celsius') {
    return Math.max(10, Math.ceil(value / 10) * 10);
  }
  if (value <= 0) {
    return 1;
  }
  const power = 10 ** Math.floor(Math.log10(value));
  const nice = [1, 2, 5, 10].find((n) => n * power >= value) ?? 10;
  return nice * power;
}

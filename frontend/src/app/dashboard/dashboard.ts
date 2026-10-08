import { DecimalPipe, NgTemplateOutlet, formatDate, formatNumber } from '@angular/common';
import { HttpErrorResponse } from '@angular/common/http';
import { Component, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { EMPTY, catchError, exhaustMap, of, switchMap, tap } from 'rxjs';

import { HubConnection } from '../connection/connection';
import { Availability, DeviceService, LOCAL_DEVICE, deviceName } from '../devices/devices';
import { I18n } from '../i18n/i18n';
import { EvenColumns } from '../layout/even-columns';
import { BytesPipe } from '../metrics/bytes.pipe';
import { ExtraItem, formatExtra, localized } from '../metrics/extras';
import {
  MetricsService,
  NetworkInterface,
  Snapshot,
  Throttling,
  ThrottlingCondition,
  TimeZone,
} from '../metrics/metrics';
import { PageVisibility } from '../page-visibility';
import { FitLabels } from './fit-labels';

/** How often the dashboard asks the backend for new values. */
export const REFRESH_INTERVAL_MS = 2000;

/** How often the availability of another device is read again. */
export const AVAILABILITY_REFRESH_MS = 10000;

/** The most core bars in one row; more cores are split evenly over several rows. */
const MAX_CORES_PER_ROW = 8;

/**
 * How many core bars go in one row: up to 8 stay in one row, more are split
 * evenly over as few rows as keep each at 8 or fewer (12 → 2 × 6, 20 → 7 + 7 + 6).
 */
export function coreColumns(cores: number): number {
  const rows = Math.ceil(cores / MAX_CORES_PER_ROW);
  return Math.ceil(cores / rows);
}

/** Why no new values arrived: the backend did not answer, or the device it collects from did not. */
type Problem = 'backend' | 'device';

/** Shows the current usage of the picked device and refreshes it every few seconds. */
@Component({
  selector: 'app-dashboard',
  imports: [BytesPipe, DecimalPipe, EvenColumns, FitLabels, NgTemplateOutlet],
  templateUrl: './dashboard.html',
  styleUrl: './dashboard.css',
})
export class Dashboard {
  private readonly metrics = inject(MetricsService);
  private readonly devices = inject(DeviceService);
  private readonly page = inject(PageVisibility);
  /** While the hub does not answer, the banner across the page says so instead of a warning here, and the cards fade. */
  protected readonly connection = inject(HubConnection);

  protected readonly snapshot = signal<Snapshot | null>(null);
  protected readonly problem = signal<Problem | null>(null);
  protected readonly i18n = inject(I18n);
  protected readonly deviceName = computed(() => deviceName(this.devices.selected(), this.i18n));
  /** Whether the picked device is a PC or laptop, which is just not in use while it does not answer. */
  protected readonly isPC = computed(() => this.devices.selected().kind === 'pc');
  /** How long the picked device did not answer since it was added; null for this device. */
  protected readonly availability = signal<Availability | null>(null);
  protected readonly coreColumns = coreColumns;

  constructor() {
    toObservable(this.devices.selectedId)
      .pipe(
        // Another device's values are not shown while the picked one's load.
        tap(() => {
          this.snapshot.set(null);
          this.problem.set(null);
        }),
        // A tick while the previous reading is still on its way is skipped, so a
        // slow backend is not asked again and again for what it has not answered.
        switchMap((device) =>
          this.page
            .ticks(REFRESH_INTERVAL_MS)
            .pipe(
              exhaustMap(() =>
                this.metrics
                  .current(device)
                  .pipe(catchError((error: unknown) => of(problemOf(error, device)))),
              ),
            ),
        ),
        takeUntilDestroyed(),
      )
      .subscribe((result) => {
        if (typeof result === 'string') {
          this.problem.set(result);
        } else {
          this.problem.set(null);
          this.snapshot.set(result);
        }
      });

    toObservable(this.devices.selectedId)
      .pipe(
        tap(() => this.availability.set(null)),
        switchMap((device) =>
          device === LOCAL_DEVICE.id
            ? EMPTY
            : this.page
                .ticks(AVAILABILITY_REFRESH_MS)
                .pipe(
                  exhaustMap(() => this.devices.availability(device).pipe(catchError(() => EMPTY))),
                ),
        ),
        takeUntilDestroyed(),
      )
      .subscribe((availability) => this.availability.set(availability));
  }

  /**
   * The share of the time the device was watched that it answered, rounded down: its
   * availability for a server, how much of the time it was in use for a PC or laptop.
   */
  protected availablePercent(a: Availability): number {
    const seconds = Math.max(1, (Date.now() - Date.parse(a.countedSince)) / 1000);
    const percent = 100 * (1 - Math.min(a.offlineSeconds, seconds) / seconds);
    return Math.floor(percent * 100) / 100;
  }

  /** The length of an outage, from its start and end as ISO times. */
  protected outageLength(outage: { start: string; end: string }): string {
    return this.formatDuration((Date.parse(outage.end) - Date.parse(outage.start)) / 1000);
  }

  /** A date and time in the page's language, from an ISO time. */
  protected when(time: string): string {
    return formatDate(time, 'short', this.i18n.language());
  }

  protected formatDuration(seconds: number): string {
    if (seconds < 60) {
      return this.i18n.t('dashboard.seconds', { seconds: Math.round(seconds) });
    }
    if (seconds < 3600) {
      return this.i18n.t('dashboard.minutes', { minutes: Math.floor(seconds / 60) });
    }
    return this.formatUptime(seconds);
  }

  /**
   * The interfaces that carried traffic since the machine started, busiest first, and how many
   * others are left out. Machines such as Windows PCs list many adapters that are never used.
   */
  protected activeInterfaces(s: Snapshot): { shown: NetworkInterface[]; idle: number } {
    const total = (n: NetworkInterface) => n.receivedBytes + n.sentBytes;
    const shown = s.network
      .filter((n) => total(n) > 0)
      .sort((a, b) => total(b) - total(a) || a.name.localeCompare(b.name));
    return { shown, idle: s.network.length - shown.length };
  }

  /** A title or label of an extra in the page's language. */
  protected extraText(text: string, texts?: Record<string, string>): string {
    return localized(text, texts, this.i18n.language());
  }

  /** The value of an extra with its unit, in the page's language. */
  protected extraValue(item: ExtraItem): string {
    return item.unit === 'text' || item.value === undefined
      ? (item.text ?? '')
      : formatExtra(item.value, item.unit, this.i18n.language());
  }

  /** A number in the page's language, for text parameters (the number pipe may return null). */
  protected decimal(value: number, digits: string): string {
    return formatNumber(value, this.i18n.language(), digits);
  }

  /** The addresses of an interface and the speed it is connected at, as one line. */
  protected linkDetails(n: NetworkInterface): string {
    const parts = [...(n.addresses ?? [])];
    if (n.linkMbps) {
      parts.push(
        n.linkMbps >= 1000
          ? this.i18n.t('dashboard.linkGbps', { speed: this.decimal(n.linkMbps / 1000, '1.0-1') })
          : this.i18n.t('dashboard.linkMbps', { speed: n.linkMbps }),
      );
    }
    return parts.join(' · ');
  }

  /** Describes each core's usage for screen readers, which cannot see the bars. */
  protected coresLabel(usage: number[]): string {
    const cores = usage.map((u, i) =>
      this.i18n.t('dashboard.coreUsage', { core: i + 1, usage: this.decimal(u, '1.0-0') }),
    );
    return `${this.i18n.t('dashboard.coresUsage')}: ${cores.join(', ')}`;
  }

  /** The conditions that held since the start but do not hold now. */
  protected earlierConditions(t: Throttling): ThrottlingCondition[] {
    return t.sinceBoot.filter((condition) => !t.now.includes(condition));
  }

  protected conditionList(conditions: ThrottlingCondition[]): string {
    return conditions.map((c) => this.i18n.t(`dashboard.throttling.${c}`)).join(', ');
  }

  /** The time of a reading as the device's own clock shows it, in the device's time zone. */
  protected deviceTime(time: string, zone: TimeZone, format: string): string {
    return formatDate(time, format, this.i18n.language(), offsetParts(zone.offsetSeconds).join(''));
  }

  /** The zone's name with its distance from UTC, such as "CEST (UTC+2)" or "IST (UTC+5:30)". */
  protected zoneLabel(zone: TimeZone): string {
    if (zone.offsetSeconds === 0) {
      return 'UTC';
    }
    const [sign, hours, minutes] = offsetParts(zone.offsetSeconds);
    const offset = `UTC${sign}${Number(hours)}${minutes === '00' ? '' : `:${minutes}`}`;
    // Zones without a short name are named by their offset, such as "+03".
    return /^[+-]/.test(zone.name) ? offset : `${zone.name} (${offset})`;
  }

  protected formatUptime(seconds: number): string {
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    return days > 0
      ? this.i18n.t('dashboard.uptimeDays', { days, hours })
      : this.i18n.t('dashboard.uptimeHours', { hours, minutes });
  }
}

/** The sign, hours and minutes of an offset from UTC, such as ["+", "02", "00"]. */
function offsetParts(seconds: number): [string, string, string] {
  const minutes = Math.abs(Math.round(seconds / 60));
  const pad = (n: number) => String(n).padStart(2, '0');
  return [seconds < 0 ? '-' : '+', pad(Math.floor(minutes / 60)), pad(minutes % 60)];
}

/** The backend answers 503 when a device it collects from has not answered recently. */
function problemOf(error: unknown, device: string): Problem {
  const deviceDown =
    device !== LOCAL_DEVICE.id && error instanceof HttpErrorResponse && error.status === 503;
  return deviceDown ? 'device' : 'backend';
}

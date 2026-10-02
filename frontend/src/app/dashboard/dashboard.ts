import { DecimalPipe } from '@angular/common';
import { HttpErrorResponse } from '@angular/common/http';
import { Component, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { MatCardModule } from '@angular/material/card';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { catchError, of, switchMap, tap, timer } from 'rxjs';

import { DeviceService, LOCAL_DEVICE, deviceName } from '../devices/devices';
import { I18n } from '../i18n/i18n';
import { BytesPipe } from '../metrics/bytes.pipe';
import { MetricsService, Snapshot } from '../metrics/metrics';

/** How often the dashboard asks the backend for new values. */
export const REFRESH_INTERVAL_MS = 2000;

/** Why no new values arrived: the backend did not answer, or the device it collects from did not. */
type Problem = 'backend' | 'device';

/** Shows the current usage of the picked device and refreshes it every few seconds. */
@Component({
  selector: 'app-dashboard',
  imports: [BytesPipe, DecimalPipe, MatCardModule, MatProgressBarModule],
  templateUrl: './dashboard.html',
  styleUrl: './dashboard.css',
})
export class Dashboard {
  private readonly metrics = inject(MetricsService);
  private readonly devices = inject(DeviceService);

  protected readonly snapshot = signal<Snapshot | null>(null);
  protected readonly problem = signal<Problem | null>(null);
  protected readonly i18n = inject(I18n);
  protected readonly deviceName = computed(() => deviceName(this.devices.selected(), this.i18n));

  constructor() {
    toObservable(this.devices.selectedId)
      .pipe(
        // Another device's values are not shown while the picked one's load.
        tap(() => {
          this.snapshot.set(null);
          this.problem.set(null);
        }),
        switchMap((device) =>
          timer(0, REFRESH_INTERVAL_MS).pipe(
            switchMap(() =>
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

/** The backend answers 503 when a device it collects from has not answered recently. */
function problemOf(error: unknown, device: string): Problem {
  const deviceDown =
    device !== LOCAL_DEVICE.id && error instanceof HttpErrorResponse && error.status === 503;
  return deviceDown ? 'device' : 'backend';
}

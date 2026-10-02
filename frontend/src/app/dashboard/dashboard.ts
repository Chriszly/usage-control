import { DecimalPipe } from '@angular/common';
import { Component, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { catchError, of, switchMap, timer } from 'rxjs';

import { BytesPipe } from '../metrics/bytes.pipe';
import { MetricsService, Snapshot } from '../metrics/metrics';

/** How often the dashboard asks the backend for new values. */
export const REFRESH_INTERVAL_MS = 2000;

/** Shows the current usage of the machine and refreshes it every few seconds. */
@Component({
  selector: 'app-dashboard',
  imports: [BytesPipe, DecimalPipe],
  templateUrl: './dashboard.html',
  styleUrl: './dashboard.css',
})
export class Dashboard {
  private readonly metrics = inject(MetricsService);

  protected readonly snapshot = signal<Snapshot | null>(null);
  protected readonly unreachable = signal(false);

  constructor() {
    timer(0, REFRESH_INTERVAL_MS)
      .pipe(
        switchMap(() => this.metrics.current().pipe(catchError(() => of(null)))),
        takeUntilDestroyed(),
      )
      .subscribe((snapshot) => {
        this.unreachable.set(snapshot === null);
        if (snapshot) {
          this.snapshot.set(snapshot);
        }
      });
  }

  protected formatUptime(seconds: number): string {
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    return days > 0 ? `${days} d ${hours} h` : `${hours} h ${minutes} min`;
  }
}

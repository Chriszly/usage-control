import { HttpErrorResponse, HttpInterceptorFn, HttpResponse } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { tap } from 'rxjs';

/**
 * Whether the hub the page was loaded from still answers. All values, those of
 * other devices too, come through it, so while it does not answer nothing on
 * the page is live. The page's own polls keep asking it, and the first answer
 * ends that state; nothing extra is sent to find out.
 */
@Injectable({ providedIn: 'root' })
export class HubConnection {
  /** When the hub stopped answering; null while it answers. */
  readonly lostSince = signal<Date | null>(null);
  readonly lost = computed(() => this.lostSince() !== null);

  /** Notes a request the hub did not answer; the time of the first one is kept. */
  failed(): void {
    if (!this.lostSince()) {
      this.lostSince.set(new Date());
    }
  }

  /** Notes an answer from the hub, an error it sent back too. */
  answered(): void {
    if (this.lostSince()) {
      this.lostSince.set(null);
    }
  }
}

/**
 * Tells the HubConnection whether each request reached the hub. Without an
 * answer the browser reports status 0, and a proxy in front of the hub 502 or
 * 504; any other status was sent by the hub itself.
 */
export const hubConnectionInterceptor: HttpInterceptorFn = (request, next) => {
  const connection = inject(HubConnection);
  return next(request).pipe(
    tap({
      next: (event) => {
        if (event instanceof HttpResponse) {
          connection.answered();
        }
      },
      error: (error: unknown) => {
        if (error instanceof HttpErrorResponse && [0, 502, 504].includes(error.status)) {
          connection.failed();
        } else {
          connection.answered();
        }
      },
    }),
  );
};

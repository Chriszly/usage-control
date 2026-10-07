import { HttpErrorResponse, HttpInterceptorFn, HttpResponse } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { TimeoutError, tap, timeout } from 'rxjs';

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
 * How long the hub has to answer a request to the API. The server cuts off an
 * answer after 10 seconds, so a request still open after 15 got no answer:
 * without a limit, a hub that lost power while the browser kept a connection
 * to it open leaves the request hanging for many minutes, and the polls that
 * wait for it never notice the hub is gone.
 */
export const ANSWER_TIMEOUT_MS = 15_000;

/**
 * Tells the HubConnection whether each request reached the hub. Without an
 * answer the browser reports status 0, and a proxy in front of the hub 502 or
 * 504; any other status was sent by the hub itself. A request to the API that
 * gets no answer in time is given up with a TimeoutError, which counts as no
 * answer too. Only reads are given up: a change to the devices may still be
 * made after the page gave up on it, and would then look as if it failed.
 */
export const hubConnectionInterceptor: HttpInterceptorFn = (request, next) => {
  const connection = inject(HubConnection);
  const sent = next(request);
  // The request is sent at once; the time runs until the answer arrives.
  const limited =
    request.method === 'GET' && request.url.startsWith('/api/')
      ? sent.pipe(timeout({ each: ANSWER_TIMEOUT_MS }))
      : sent;
  return limited.pipe(
    tap({
      next: (event) => {
        if (event instanceof HttpResponse) {
          connection.answered();
        }
      },
      error: (error: unknown) => {
        if (
          error instanceof TimeoutError ||
          (error instanceof HttpErrorResponse && [0, 502, 504].includes(error.status))
        ) {
          connection.failed();
        } else {
          connection.answered();
        }
      },
    }),
  );
};

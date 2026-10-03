import { DOCUMENT, Injectable, inject } from '@angular/core';
import {
  EMPTY,
  Observable,
  distinctUntilChanged,
  fromEvent,
  map,
  startWith,
  switchMap,
  timer,
} from 'rxjs';

/**
 * Tells whether the page is shown. A tab in the background or a minimized
 * window is hidden; the polls pause then, so a tab nobody looks at costs the
 * device nothing, and read again as soon as the page is shown.
 */
@Injectable({ providedIn: 'root' })
export class PageVisibility {
  private readonly document = inject(DOCUMENT);

  /** Emits whether the page is shown, at once and whenever that changes. */
  readonly visible: Observable<boolean> = fromEvent(this.document, 'visibilitychange').pipe(
    startWith(null),
    map(() => this.document.visibilityState !== 'hidden'),
    distinctUntilChanged(),
  );

  /**
   * Emits at once and then every intervalMs while the page is shown. While it
   * is hidden nothing is emitted; when it is shown again, the first tick comes
   * at once.
   */
  ticks(intervalMs: number): Observable<number> {
    return this.visible.pipe(switchMap((visible) => (visible ? timer(0, intervalMs) : EMPTY)));
  }
}

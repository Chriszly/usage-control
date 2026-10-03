import { HttpClient } from '@angular/common/http';
import { Component, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { catchError, of, switchMap, timer } from 'rxjs';

import { I18n } from '../i18n/i18n';

/** The body of GET /api/update. */
export interface UpdateStatus {
  /** The version running on this device. */
  current: string;
  /** The newest release, only when it is newer than the running version. */
  latest?: string;
  /** The page of that release, with what changed. */
  url?: string;
}

/** How often the page asks whether a newer release exists; the backend asks GitHub once a day. */
export const UPDATE_REFRESH_MS = 60 * 60 * 1000;

/** Names a newer release when the backend found one, with a link to what changed. */
@Component({
  selector: 'app-update-notice',
  templateUrl: './update-notice.html',
  styleUrl: './update-notice.css',
})
export class UpdateNotice {
  protected readonly i18n = inject(I18n);
  protected readonly status = signal<UpdateStatus | null>(null);

  constructor() {
    const http = inject(HttpClient);
    timer(0, UPDATE_REFRESH_MS)
      .pipe(
        // Older backends have no /api/update; then nothing is shown.
        switchMap(() => http.get<UpdateStatus>('/api/update').pipe(catchError(() => of(null)))),
        takeUntilDestroyed(),
      )
      .subscribe((status) => this.status.set(status));
  }
}

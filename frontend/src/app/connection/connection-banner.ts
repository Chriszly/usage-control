import { formatDate } from '@angular/common';
import { Component, computed, inject } from '@angular/core';

import { DeviceService, hubName } from '../devices/devices';
import { I18n } from '../i18n/i18n';
import { HubConnection } from './connection';

/**
 * Says across the page that the hub does not answer, so nothing shown is live.
 * It goes away by itself as soon as the hub answers again.
 */
@Component({
  selector: 'app-connection-banner',
  templateUrl: './connection-banner.html',
  styleUrl: './connection-banner.css',
})
export class ConnectionBanner {
  protected readonly connection = inject(HubConnection);
  protected readonly i18n = inject(I18n);
  private readonly devices = inject(DeviceService);

  /** The hub's name, as its button shows it. */
  protected readonly hub = computed(() => hubName(this.devices.devices(), this.i18n));

  /** When the hub stopped answering, in the page's language. */
  protected readonly since = computed(() => {
    const since = this.connection.lostSince();
    return since ? formatDate(since, this.i18n.t('format.time'), this.i18n.language()) : '';
  });
}

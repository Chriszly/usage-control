import { formatDate } from '@angular/common';
import { Component, Injector, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { EMPTY, catchError, exhaustMap } from 'rxjs';

import { I18n } from '../i18n/i18n';
import { PageVisibility } from '../page-visibility';
import { Device, DeviceService, deviceName } from './devices';

/** How often the list is read again, so the buttons show which devices answer. */
export const DEVICES_REFRESH_MS = 5000;

/**
 * Chips to pick the device whose usage is shown, once the hub collects from
 * other devices, and a button that opens the list of devices to add or remove one.
 */
@Component({
  selector: 'app-device-picker',
  imports: [MatButtonModule],
  templateUrl: './device-picker.html',
  styleUrl: './device-picker.css',
})
export class DevicePicker {
  protected readonly devices = inject(DeviceService);
  protected readonly i18n = inject(I18n);
  private readonly injector = inject(Injector);

  constructor() {
    // Without the list only this device is shown.
    inject(PageVisibility)
      .ticks(DEVICES_REFRESH_MS)
      .pipe(
        exhaustMap(() => this.devices.load().pipe(catchError(() => EMPTY))),
        takeUntilDestroyed(),
      )
      .subscribe();
  }

  /** The dialogs are loaded only when they are opened, so the page loads fast. */
  protected async openDevices(): Promise<void> {
    const { openDevicesDialog } = await import('./devices-dialog');
    openDevicesDialog(this.injector);
  }

  protected deviceName(device: Device): string {
    return deviceName(device, this.i18n);
  }

  /** Whether the device answers, and since when it does not. */
  protected status(device: Device): string {
    if (!device.unreachable) {
      return this.i18n.t('devices.reachable');
    }
    if (!device.unreachableSince) {
      return this.i18n.t('devices.unreachable');
    }
    const since = formatDate(device.unreachableSince, 'short', this.i18n.language());
    return this.i18n.t('devices.unreachableSince', { since });
  }
}

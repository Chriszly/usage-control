import { formatDate } from '@angular/common';
import { Component, Injector, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { EMPTY, catchError, exhaustMap } from 'rxjs';

import { HubConnection } from '../connection/connection';
import { I18n } from '../i18n/i18n';
import { PageVisibility } from '../page-visibility';
import { Device, DeviceService, LOCAL_DEVICE, deviceName, hubName, notInUse } from './devices';

/**
 * The dot before a device's name. While the hub does not answer, the hub is
 * unreachable and nothing is known about the devices it collects from.
 */
type DeviceState = 'reachable' | 'unreachable' | 'notInUse' | 'unknown';

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
  private readonly connection = inject(HubConnection);

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

  protected state(device: Device): DeviceState {
    if (this.connection.lost()) {
      return device.id === LOCAL_DEVICE.id ? 'unreachable' : 'unknown';
    }
    if (!device.unreachable) {
      return 'reachable';
    }
    return notInUse(device) ? 'notInUse' : 'unreachable';
  }

  /** For screen readers, after the name of a device that does not answer. */
  protected shortStatus(device: Device): string {
    const state = this.state(device);
    return state === 'unknown' ? this.status(device) : this.i18n.t(`devices.${state}`);
  }

  /** Whether the device answers, and since when it does not or is not in use. */
  protected status(device: Device): string {
    const lostSince = this.connection.lostSince();
    if (lostSince) {
      if (device.id !== LOCAL_DEVICE.id) {
        return this.i18n.t('devices.unknown', { hub: hubName(this.devices.devices(), this.i18n) });
      }
      const since = formatDate(lostSince, 'short', this.i18n.language());
      return this.i18n.t('devices.unreachableSince', { since });
    }
    if (!device.unreachable) {
      return this.i18n.t('devices.reachable');
    }
    const idle = notInUse(device);
    if (!device.unreachableSince) {
      return this.i18n.t(idle ? 'devices.notInUse' : 'devices.unreachable');
    }
    const since = formatDate(device.unreachableSince, 'short', this.i18n.language());
    return this.i18n.t(idle ? 'devices.notInUseSince' : 'devices.unreachableSince', { since });
  }
}

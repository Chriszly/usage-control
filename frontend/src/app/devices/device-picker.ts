import { Component, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { EMPTY, catchError } from 'rxjs';

import { I18n } from '../i18n/i18n';
import { Device, DeviceService, deviceName } from './devices';

/**
 * Buttons to pick the device whose usage is shown. Only shown in hub mode,
 * when the backend collects from other devices too.
 */
@Component({
  selector: 'app-device-picker',
  imports: [MatButtonToggleModule],
  templateUrl: './device-picker.html',
  styleUrl: './device-picker.css',
})
export class DevicePicker {
  protected readonly devices = inject(DeviceService);
  protected readonly i18n = inject(I18n);

  constructor() {
    // Without the list only this device is shown, as without hub mode.
    this.devices
      .load()
      .pipe(
        catchError(() => EMPTY),
        takeUntilDestroyed(),
      )
      .subscribe((devices) => this.devices.devices.set(devices));
  }

  protected deviceName(device: Device): string {
    return deviceName(device, this.i18n);
  }
}
